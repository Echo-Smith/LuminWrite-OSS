// WebSocket end-to-end test for Writing Agent V2 - Chat Intent
// Usage: node e2e-chat-test.mjs
import WebSocket from 'ws';

const message = '你好，今天天气怎么样？';
const wsUrl = 'ws://localhost:8080/api/v2/ws/agent';

console.log(`Connecting to ${wsUrl}...`);
console.log(`Message: ${message}\n`);

const ws = new WebSocket(wsUrl);

ws.on('open', () => {
  console.log('✅ WebSocket connected');

  // Send agent.start with auto mode (intent will be classified as "chat")
  ws.send(JSON.stringify({
    type: 'agent.start',
    payload: {
      message,
      style: 'yinyue',
      mode: 'auto',
    },
  }));
  console.log('→ agent.start sent\n');
});

let streamChunks = 0;
let fullText = '';
let stepHistory = [];

ws.on('message', (data) => {
  const msg = JSON.parse(data.toString());
  const { type, payload } = msg;

  switch (type) {
    case 'agent.created':
      console.log(`✅ agent.created — trace_id: ${payload.trace_id}, style: ${payload.style}, mode: ${payload.mode}\n`);
      break;

    case 'agent.step.start':
      console.log(`▶️  step.start: ${payload.step} (index: ${payload.step_index})`);
      stepHistory.push({ step: payload.step, status: 'running', index: payload.step_index });
      break;

    case 'agent.step.complete':
      console.log(`✅ step.complete: ${payload.step} (${payload.duration_ms}ms)`);
      if (payload.step === 'intent' && payload.result) {
        console.log(`   intent: ${payload.result.taskMode} (confidence: ${payload.result.confidence}, source: ${payload.result.source})`);
      }
      // Update step status
      const stepRec = stepHistory.find(s => s.step === payload.step);
      if (stepRec) stepRec.status = 'complete';
      break;

    case 'agent.stream':
      if (streamChunks === 0) {
        process.stdout.write('\n📝 Chat response streaming:\n---\n');
      }
      process.stdout.write(payload.delta);
      fullText += payload.delta;
      streamChunks++;
      break;

    case 'agent.stream.done':
      console.log(`\n---\n✅ stream.done (${streamChunks} chunks, ${payload.full_text.length} chars)\n`);
      break;

    case 'agent.await_input':
      console.log(`⏸️  await_input: step=${payload.step}`);
      break;

    case 'agent.paused':
      console.log(`⏸️  agent.paused`);
      break;

    case 'agent.resumed':
      console.log(`▶️  agent.resumed`);
      break;

    case 'agent.completed':
      console.log(`🎉 agent.completed`);
      console.log(`   Response length: ${payload.article?.length} chars`);
      console.log(`   Review passed: ${payload.review?.passed}`);
      console.log(`   Token usage:`, payload.token_usage);

      // Verify that chat mode worked correctly
      console.log('\n═══════════════════════════════════════');
      console.log('📊 Test Summary:');
      console.log(`   ✅ Steps executed: ${stepHistory.length}`);
      console.log(`   ✅ Step sequence: ${stepHistory.map(s => s.step).join(' → ')}`);

      const intentStep = stepHistory.find(s => s.step === 'intent');
      if (intentStep) {
        console.log(`   ✅ Intent step: ${intentStep.status}`);
      }

      const chatStep = stepHistory.find(s => s.step === 'chat');
      if (chatStep) {
        console.log(`   ✅ Chat step: ${chatStep.status}`);
      }

      const writeStep = stepHistory.find(s => s.step === 'write');
      const reviewStep = stepHistory.find(s => s.step === 'post_review');
      const autoFixStep = stepHistory.find(s => s.step === 'auto_fix');

      if (!writeStep && !reviewStep && !autoFixStep) {
        console.log(`   ✅ Writing steps correctly skipped (intent=chat)`);
      } else {
        console.log(`   ❌ ERROR: Writing steps should be skipped for chat intent`);
        console.log(`      write: ${writeStep ? writeStep.status : 'N/A'}`);
        console.log(`      post_review: ${reviewStep ? reviewStep.status : 'N/A'}`);
        console.log(`      auto_fix: ${autoFixStep ? autoFixStep.status : 'N/A'}`);
      }

      console.log(`\n📄 Response preview (first 100 chars):\n${fullText.slice(0, 100)}...`);
      ws.close();
      break;

    case 'agent.error':
      console.log(`❌ agent.error: ${payload.message} (code: ${payload.code})`);
      ws.close();
      break;

    case 'agent.cancelled':
      console.log(`❌ agent.cancelled`);
      ws.close();
      break;

    default:
      console.log(`⚠️  Unknown message type: ${type}`);
  }
});

ws.on('error', (err) => {
  console.error('❌ WebSocket error:', err);
  process.exit(1);
});

ws.on('close', (code, reason) => {
  if (code !== 1000) {
    console.log(`❌ WebSocket closed: ${code} - ${reason}`);
  } else {
    console.log('\n✅ WebSocket connection closed normally');
  }
});

// Timeout after 30 seconds
setTimeout(() => {
  console.log('\n⏰ Timeout after 30 seconds');
  ws.close();
  process.exit(1);
}, 30000);