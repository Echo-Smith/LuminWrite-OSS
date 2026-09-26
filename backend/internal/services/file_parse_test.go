package services

import (
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

// startFakeDocreader 启动一个假 docreader TCP 服务，返回地址与请求记录通道。
// handler 收到解析出的 (ext, bytes) 后返回响应文本；收到的请求经通道交付
// （带缓冲，先于响应写入），避免测试断言与 goroutine 之间的数据竞争。
func startFakeDocreader(t *testing.T, handler func(ext string, content []byte) string) (addr string, requests chan fakeDocreaderRequest) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	requests = make(chan fakeDocreaderRequest, 4)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				// 逐字节读 header 直到 '\n'——bufio 缓冲会吞掉 body 的前几个字节，
				// 导致后续按长度读 body 时死锁。
				var line []byte
				one := make([]byte, 1)
				for {
					if _, err := c.Read(one); err != nil {
						return
					}
					if one[0] == '\n' {
						break
					}
					line = append(line, one[0])
				}
				var size int
				var ext string
				if _, err := fmt.Sscanf(string(line), "PARSEBYTES %d %s", &size, &ext); err != nil {
					return
				}
				body := make([]byte, size)
				if _, err := ioReadFull(c, body); err != nil {
					return
				}
				requests <- fakeDocreaderRequest{ext: ext, body: body}
				fmt.Fprint(c, handler(ext, body))
			}(conn)
		}
	}()
	return ln.Addr().String(), requests
}

type fakeDocreaderRequest struct {
	ext  string
	body []byte
}

func awaitRequest(t *testing.T, requests <-chan fakeDocreaderRequest) fakeDocreaderRequest {
	t.Helper()
	select {
	case r := <-requests:
		return r
	case <-time.After(2 * time.Second):
		t.Fatal("fake docreader did not record a request in time")
		return fakeDocreaderRequest{}
	}
}

func ioReadFull(c net.Conn, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := c.Read(buf[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

func TestFileParserParseWithDocreaderSendsInlineBytes(t *testing.T) {
	addr, requests := startFakeDocreader(t, func(ext string, content []byte) string {
		return "解析后的正文内容，足够长以通过最短长度校验。"
	})
	f := NewFileParser(nil, ChunkConfig{}, addr)

	content, err := f.callDocreaderTCP(context.Background(), ".pdf", []byte("%PDF-1.4 fake bytes"))
	if err != nil {
		t.Fatalf("callDocreaderTCP: %v", err)
	}
	if !strings.Contains(content, "解析后的正文") {
		t.Fatalf("unexpected content: %q", content)
	}
	req := awaitRequest(t, requests)
	if req.ext != ".pdf" {
		t.Fatalf("ext = %q", req.ext)
	}
	if string(req.body) != "%PDF-1.4 fake bytes" {
		t.Fatalf("bytes not transmitted intact: %q", req.body)
	}
}

func TestFileParserCallDocreaderTCPRejectsErrorResponse(t *testing.T) {
	addr, _ := startFakeDocreader(t, func(ext string, content []byte) string {
		return "ERROR: scanned document, no text layer"
	})
	f := NewFileParser(nil, ChunkConfig{}, addr)

	_, err := f.callDocreaderTCP(context.Background(), ".pdf", []byte("%PDF-1.4"))
	if err == nil || !strings.Contains(err.Error(), "docreader rejected document") {
		t.Fatalf("want typed rejection, got %v", err)
	}
}

func TestFileParserParseWithDocreaderRejectsOversizeBeforeDial(t *testing.T) {
	// 指向不可达地址：若实现错误地先建连，这里会以连接错误而非超限错误失败。
	f := NewFileParser(nil, ChunkConfig{}, "127.0.0.1:1")
	big := make([]byte, docreaderMaxInlineBytes+1)

	_, err := f.parseWithDocreader(context.Background(), "big.pdf", strings.NewReader(string(big)))
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("want oversize rejection before dial, got %v", err)
	}
}
