// WebSocket end-to-end test for Writing Agent V2
// Usage: node e2e-test.mjs [message]
import WebSocket from 'ws';

const message = process.argv[2] || '基于热搜写一篇关于外卖骑手闯红灯的评论';
const wsUrl = 'ws://localhost:8080/api/v2/ws/agent';

console.log(`Connecting to ${wsUrl}...`);
console.log(`Message: ${message}\n`);

const ws = new WebSocket(wsUrl);

ws.on('open', () => {
  console.log('✅ WebSocket connected');

  // Send agent.start
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
let fullArticle = '';

ws.on('message', (data) => {
  const msg = JSON.parse(data.toString());
  const { type, payload } = msg;

  switch (type) {
    case 'agent.created':
      console.log(`✅ agent.created — trace_id: ${payload.trace_id}, style: ${payload.style}, mode: ${payload.mode}\n`);
      break;

    case 'agent.step.start':
      console.log(`▶️  step.start: ${payload.step} (index: ${payload.step_index})`);
      break;

    case 'agent.step.complete':
      console.log(`✅ step.complete: ${payload.step} (${payload.duration_ms}ms)`);
      if (payload.step === 'intent' && payload.result) {
        console.log(`   intent: ${payload.result.taskMode} (confidence: ${payload.result.confidence}, source: ${payload.result.source})`);
      }
      if (payload.step === 'search' && payload.result) {
        console.log(`   search results: ${payload.result.count}`);
      }
      break;

    case 'agent.stream':
      if (streamChunks === 0) {
        process.stdout.write('\n📝 Article streaming:\n---\n');
      }
      process.stdout.write(payload.delta);
      fullArticle += payload.delta;
      streamChunks++;
      break;

    case 'agent.stream.done':
      console.log(`\n---\n✅ stream.done (${streamChunks} chunks, ${payload.full_text.length} chars)\n`);
      break;

    case 'agent.await_input':
      console.log(`⏸️  await_input: step=${payload.step}`);
      console.log(`   data: ${JSON.stringify(payload.data, null, 2)}`);
      console.log(`   options: ${payload.options.join(', ')}`);

      // Auto-confirm after 1 second
      setTimeout(() => {
        ws.send(JSON.stringify({
          type: 'agent.confirm',
          payload: {
            trace_id: payload.trace_id,
            step: payload.step,
            data: payload.data, // confirm with original data
          },
        }));
        console.log('→ agent.confirm sent\n');
      }, 1000);
      break;

    case 'agent.completed':
      console.log(`🎉 agent.completed!`);
      console.log(`   article length: ${payload.article.length} chars`);
      console.log(`   review: ${JSON.stringify(payload.review, null, 2)}`);
      console.log(`   token usage: ${JSON.stringify(payload.token_usage)}`);
      ws.close();
      process.exit(0);
      break;

    case 'agent.error':
      console.error(`❌ agent.error: [${payload.code}] ${payload.message} (step: ${payload.step})`);
      ws.close();
      process.exit(1);
      break;

    case 'agent.cancelled':
      console.log('🚫 agent.cancelled');
      ws.close();
      process.exit(0);
      break;

    default:
      console.log(`📩 ${type}: ${JSON.stringify(payload).slice(0, 200)}`);
  }
});

ws.on('error', (err) => {
  console.error('WebSocket error:', err.message);
  process.exit(1);
});

ws.on('close', () => {
  console.log('\n🔌 WebSocket closed');
});

// Timeout after 120s
setTimeout(() => {
  console.error('\n⏱️  Timeout after 120s');
  ws.close();
  process.exit(1);
}, 120000);
