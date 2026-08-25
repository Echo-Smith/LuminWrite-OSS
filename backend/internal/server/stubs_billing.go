package server

import (
	"context"
	"net/http"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/response"
)

// ── Alipay payment stubs (open-source version) ──
// These are placeholder implementations for the commercial Alipay payment
// features. They return "not available" responses in the open-source build.

// AlipayService is a stub for the commercial Alipay payment service.
type AlipayService struct{}

// AlipayConfigConfig is a stub config type.
type AlipayConfigConfig = struct {
	Enabled        bool
	AppID          string
	PrivateKey     string
	PublicKey      string
	CertPath       string
	RootCertPath   string
	AlipayCertPath string
	NotifyURL      string
	ReturnURL      string
	Sandbox        bool
}

// NewAlipayService creates a nil AlipayService (stub).
func NewAlipayService(cfg AlipayConfigConfig, _ interface{}) (*AlipayService, error) {
	return nil, nil
}

// handleAlipayCreatePayment stub
func (s *Server) handleAlipayCreatePayment(w http.ResponseWriter, r *http.Request) {
	response.Err(w, http.StatusServiceUnavailable, "alipay_disabled", "支付宝支付未启用")
}

// handleAlipayCallback stub
func (s *Server) handleAlipayCallback(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("fail"))
}

// handleBillingOrderStatus stub
func (s *Server) handleBillingOrderStatus(w http.ResponseWriter, r *http.Request) {
	response.Err(w, http.StatusServiceUnavailable, "billing_disabled", "billing system not available")
}

// handleAdminBillingReset stub
func (s *Server) handleAdminBillingReset(w http.ResponseWriter, r *http.Request) {
	response.Err(w, http.StatusServiceUnavailable, "billing_disabled", "billing system not available")
}

// handleAdminBillingExpire stub
func (s *Server) handleAdminBillingExpire(w http.ResponseWriter, r *http.Request) {
	response.Err(w, http.StatusServiceUnavailable, "billing_disabled", "billing system not available")
}

// SettleToolPoints stub
func (s *Server) SettleToolPoints(ctx context.Context, userID, traceID, toolType string) (float64, error) {
	return 0, nil
}

// startBillingCron stub (no-op in open-source version)
func (s *Server) startBillingCron(ctx context.Context) {
	// no-op
}
