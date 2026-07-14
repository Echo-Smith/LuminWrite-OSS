// Guided Mode + Pause/Resume E2E Test for Writing Agent V2
// Usage: node e2e-guided-test.mjs
import WebSocket from 'ws';

const wsUrl = 'ws://localhost:8080/api/v2/ws/agent';
const message = '基于热搜写一篇关于外卖骑手闯红灯的评论';

console.log(`Connecting to ${wsUrl}...`);
console.log(`Message: ${message}`);
console.log(`Mode: guided\n`);

const ws = new WebSocket(wsUrl);

let traceId = null;
let streamChunks = 0;
let fullArticle = '';
let pausedDuringStream = false;
let hasReceivedAwaitInput = false;

ws.on('open', () => {
  console.log('✅ WebSocket connected');
  ws.send(JSON.stringify({
    type: 'agent.start',
    payload: { message, style: 'yinyue', mode: 'guided' },
  }));
  console.log('→ agent.start sent (guided mode)\n');
});

ws.on('message', (data) => {
  const msg = JSON.parse(data.toString());
  const { type, payload } = msg;

  switch (type) {
    case 'agent.created':
      traceId = payload.trace_id;
      console.log(`✅ agent.created — trace_id: ${traceId}\n`);
      break;

    case 'agent.step.start':
      console.log(`▶️  step.start: ${payload.step}`);
      break;

    case 'agent.step.complete':
      console.log(`✅ step.complete: ${payload.step} (${payload.duration_ms}ms)`);
      break;

    case 'agent.await_input':
      hasReceivedAwaitInput = true;
      console.log(`\n⏸️  await_input: step=${payload.step}`);
      console.log(`   Title: ${payload.data?.title}`);
      console.log(`   Outline points: ${payload.data?.outline?.length}`);
      payload.data?.outline?.forEach((item, i) => {
        console.log(`   ${i + 1}. [${item.type}] ${item.point}`);
      });
      console.log(`\n→ Sending confirm with original data in 1s...`);

      // Wait 1s then confirm with the original outline data
      setTimeout(() => {
        ws.send(JSON.stringify({
          type: 'agent.confirm',
          payload: {
            trace_id: traceId,
            step: 'outline',
            data: payload.data,
          },
        }));
        console.log('→ agent.confirm sent\n');
      }, 1000);
      break;

    case 'agent.stream':
      if (streamChunks === 0) {
        console.log('\n📝 Article streaming:\n---');
      }
      process.stdout.write(payload.delta);
      fullArticle += payload.delta;
      streamChunks++;

      // Test pause after 10 chunks
      if (streamChunks === 10 && !pausedDuringStream) {
        pausedDuringStream = true;
        console.log('\n\n⏸️  Testing pause during streaming...');
        ws.send(JSON.stringify({
          type: 'agent.pause',
          payload: { trace_id: traceId },
        }));
        console.log('→ agent.pause sent\n');

        // Resume after 2 seconds
        setTimeout(() => {
          console.log('\n▶️  Testing resume...');
          ws.send(JSON.stringify({
            type: 'agent.resume',
            payload: { trace_id: traceId },
          }));
          console.log('→ agent.resume sent\n');
        }, 2000);
      }
      break;

    case 'agent.paused':
      console.log(`\n⏸️  agent.paused: step=${payload.step}`);
      break;

    case 'agent.resumed':
      console.log(`\n▶️  agent.resumed: step=${payload.step}`);
      break;

    case 'agent.stream.done':
      console.log(`\n---\n✅ stream.done (${streamChunks} chunks, ${payload.full_text.length} chars)\n`);
      break;

    case 'agent.completed':
      console.log(`\n🎉 agent.completed`);
      console.log(`   Article length: ${payload.article?.length} chars`);
      console.log(`   Review passed: ${payload.review?.passed}`);
      console.log(`   Token usage:`, payload.token_usage);

      // Summary
      console.log('\n═══════════════════════════════════════');
      console.log('📊 Test Summary:');
      console.log(`   ✅ Guided mode await_input: ${hasReceivedAwaitInput ? 'PASS' : 'FAIL'}`);
      console.log(`   ✅ Outline confirm flow: ${streamChunks > 0 ? 'PASS' : 'FAIL'}`);
      console.log(`   ✅ Pause/Resume during stream: ${pausedDuringStream ? 'TESTED' : 'SKIPPED'}`);
      console.log(`   ✅ Article generated: ${fullArticle.length > 0 ? 'PASS' : 'FAIL'}`);
      console.log('═══════════════════════════════════════\n');

      ws.close();
      process.exit(0);
      break;

    case 'agent.error':
      console.log(`\n❌ agent.error: ${payload.code} - ${payload.message}`);
      ws.close();
      process.exit(1);
      break;

    case 'agent.cancelled':
      console.log(`\n🚫 agent.cancelled`);
      ws.close();
      process.exit(1);
      break;
  }
});

ws.on('error', (e) => {
  console.error('WebSocket error:', e);
});

// Timeout after 120 seconds
setTimeout(() => {
  console.log('\n⏱️  Test timed out after 120s');
  ws.close();
  process.exit(1);
}, 120000);
