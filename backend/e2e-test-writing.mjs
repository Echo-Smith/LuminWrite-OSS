// WebSocket end-to-end test for Writing Agent V2 — writing mode (hot search topic)
// Usage: node e2e-test-writing.mjs [message]
import WebSocket from 'ws';

const message = process.argv[2] || '基于热搜选题「外卖骑手闯红灯现象」写一篇评论文章';
const wsUrl = 'ws://localhost:8080/api/v2/ws/agent';

console.log(`Connecting to ${wsUrl}...`);
console.log(`Message: ${message}\n`);
console.log(`Mode: writing (triggers agent loop + thinking)\n`);

const ws = new WebSocket(wsUrl);

ws.on('open', () => {
  console.log('✅ WebSocket connected');

  // Send agent.start with mode: 'writing' to simulate hot search topic
  ws.send(JSON.stringify({
    type: 'agent.start',
    payload: {
      message,
      style: 'yinyue',
      mode: 'writing',
    },
  }));
  console.log('→ agent.start sent (mode: writing)\n');
});

let streamChunks = 0;
let fullArticle = '';
let reasoningChunks = 0;
let fullReasoning = '';
let stepStarted = {};

ws.on('message', (data) => {
  const msg = JSON.parse(data.toString());
  const { type, payload } = msg;

  switch (type) {
    case 'agent.created':
      console.log(`✅ agent.created — trace_id: ${payload.trace_id}, style: ${payload.style}, mode: ${payload.mode}\n`);
      break;

    case 'agent.step.start':
      console.log(`▶️  step.start: ${payload.step} (index: ${payload.step_index})`);
      stepStarted[payload.step] = Date.now();
      break;

    case 'agent.step.complete':
      {
        const dur = stepStarted[payload.step] ? Date.now() - stepStarted[payload.step] : 0;
        console.log(`✅ step.complete: ${payload.step} (${dur}ms)`);
        if (payload.step === 'intent' && payload.result) {
          console.log(`   intent: ${payload.result.taskMode} (confidence: ${payload.result.confidence})`);
        }
        if (payload.step === 'search' && payload.result) {
          console.log(`   search results: ${payload.result.count}`);
        }
      }
      break;

    case 'agent.reasoning':
      if (reasoningChunks === 0) {
        process.stdout.write('\n🧠 Reasoning streaming:\n---\n');
      }
      process.stdout.write(payload.delta);
      fullReasoning += payload.delta;
      reasoningChunks++;
      break;

    case 'agent.stream':
      if (streamChunks === 0) {
        process.stdout.write('\n\n📝 Article streaming:\n---\n');
      }
      process.stdout.write(payload.delta);
      fullArticle += payload.delta;
      streamChunks++;
      break;

    case 'agent.stream.reset':
      console.log(`\n⚠️  stream.reset — discarding ${fullArticle.length} chars of intermediate content`);
      fullArticle = '';
      streamChunks = 0;
      break;

    case 'agent.stream.done':
      console.log(`\n---\n✅ stream.done (${streamChunks} chunks, ${payload.full_text?.length || 0} chars in full_text)`);
      console.log(`   fullArticle accumulated: ${fullArticle.length} chars`);
      break;

    case 'agent.await_input':
      console.log(`\n⏸️  await_input: step=${payload.step}`);
      console.log(`   data: ${JSON.stringify(payload.data, null, 2)}`);
      console.log(`   options: ${payload.options.join(', ')}`);

      // Auto-confirm after 1 second
      setTimeout(() => {
        ws.send(JSON.stringify({
          type: 'agent.confirm',
          payload: {
            trace_id: payload.trace_id,
            step: payload.step,
            data: payload.data,
          },
        }));
        console.log('→ agent.confirm sent\n');
      }, 1000);
      break;

    case 'agent.completed':
      console.log(`\n🎉 agent.completed!`);
      console.log(`   article length: ${payload.article?.length || 0} chars`);
      console.log(`   reasoning length: ${fullReasoning.length} chars (${reasoningChunks} chunks)`);
      console.log(`   stream article length: ${fullArticle.length} chars (${streamChunks} chunks)`);
      console.log(`   review: ${JSON.stringify(payload.review, null, 2)}`);
      console.log(`   token usage: ${JSON.stringify(payload.token_usage)}`);

      // Diagnostics
      console.log('\n--- DIAGNOSTICS ---');
      if (fullArticle.length === 0 && fullReasoning.length > 0) {
        console.log('❌ BUG: Reasoning was streamed but NO article content was streamed!');
        console.log('   This means flushChunked was never called (assistantMsg.Content was empty).');
        console.log('   The model produced reasoning_content but no content in the final round.');
      } else if (fullArticle.length > 0) {
        console.log('✅ Article content was streamed successfully.');
      }
      if (fullReasoning.length > 0) {
        console.log(`   Reasoning preview (first 200 chars): ${fullReasoning.slice(0, 200)}...`);
      }
      if (fullArticle.length > 0) {
        console.log(`   Article preview (first 200 chars): ${fullArticle.slice(0, 200)}...`);
      }

      ws.close();
      process.exit(0);
      break;

    case 'agent.error':
      console.error(`\n❌ agent.error: [${payload.code}] ${payload.message} (step: ${payload.step})`);
      ws.close();
      process.exit(1);
      break;

    case 'agent.cancelled':
      console.log('\n🚫 agent.cancelled');
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

// Timeout after 180s (thinking mode takes longer)
setTimeout(() => {
  console.error('\n⏱️  Timeout after 180s');
  console.error(`   reasoning: ${fullReasoning.length} chars (${reasoningChunks} chunks)`);
  console.error(`   article: ${fullArticle.length} chars (${streamChunks} chunks)`);
  ws.close();
  process.exit(1);
}, 180000);
