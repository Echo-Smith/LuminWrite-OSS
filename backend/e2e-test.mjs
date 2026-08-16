// WebSocket end-to-end test for Writing Agent V2
// Usage:
//   node e2e-test.mjs                         # auto mode, default message
//   node e2e-test.mjs "你的消息"               # auto mode, custom message
//   node e2e-test.mjs --mode writing          # writing mode (with reasoning stream)
//   node e2e-test.mjs "你好" --mode auto      # chat intent test
//   node e2e-test.mjs --mode guided           # guided mode (delegates to e2e-guided-test)
import WebSocket from 'ws';

// ── Parse args ──────────────────────────────────────
const args = process.argv.slice(2);
const message = args.find(a => !a.startsWith('--')) || '基于热搜写一篇关于外卖骑手闯红灯的评论';
const modeIdx = args.indexOf('--mode');
const mode = modeIdx >= 0 ? args[modeIdx + 1] : 'auto';

const wsUrl = process.env.WS_URL || 'ws://localhost:8080/api/v2/ws/agent';
const timeout = mode === 'writing' ? 180000 : 120000;

console.log(`Connecting to ${wsUrl}...`);
console.log(`Message: ${message}`);
console.log(`Mode: ${mode}\n`);

// ── State ───────────────────────────────────────────
const ws = new WebSocket(wsUrl);
let streamChunks = 0;
let fullArticle = '';
let reasoningChunks = 0;
let fullReasoning = '';
let stepHistory = [];
let stepStarted = {};

ws.on('open', () => {
  console.log('✅ WebSocket connected');
  ws.send(JSON.stringify({
    type: 'agent.start',
    payload: { message, style: 'yinyue', mode },
  }));
  console.log(`→ agent.start sent (mode: ${mode})\n`);
});

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
      stepStarted[payload.step] = Date.now();
      break;

    case 'agent.step.complete':
      {
        const dur = stepStarted[payload.step] ? Date.now() - stepStarted[payload.step] : payload.duration_ms;
        console.log(`✅ step.complete: ${payload.step} (${dur}ms)`);
        if (payload.step === 'intent' && payload.result) {
          console.log(`   intent: ${payload.result.taskMode} (confidence: ${payload.result.confidence}, source: ${payload.result.source})`);
        }
        if (payload.step === 'search' && payload.result) {
          console.log(`   search results: ${payload.result.count}`);
        }
        const rec = stepHistory.find(s => s.step === payload.step);
        if (rec) rec.status = 'complete';
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
        process.stdout.write('\n📝 Streaming:\n---\n');
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
      console.log(`\n---\n✅ stream.done (${streamChunks} chunks, ${payload.full_text?.length || 0} chars)\n`);
      break;

    case 'agent.await_input':
      console.log(`\n⏸️  await_input: step=${payload.step}`);
      console.log(`   data: ${JSON.stringify(payload.data, null, 2)}`);
      console.log(`   options: ${payload.options?.join(', ')}`);

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

    case 'agent.paused':
      console.log(`\n⏸️  agent.paused: step=${payload.step}`);
      break;

    case 'agent.resumed':
      console.log(`\n▶️  agent.resumed: step=${payload.step}`);
      break;

    case 'agent.completed':
      console.log(`\n🎉 agent.completed!`);
      console.log(`   article length: ${payload.article?.length || 0} chars`);
      console.log(`   reasoning length: ${fullReasoning.length} chars (${reasoningChunks} chunks)`);
      console.log(`   review: ${JSON.stringify(payload.review, null, 2)}`);
      console.log(`   token usage: ${JSON.stringify(payload.token_usage)}`);

      // ── Diagnostics ────────────────────────────────
      console.log('\n═══════════════════════════════════════');
      console.log('📊 Test Summary:');
      console.log(`   Steps executed: ${stepHistory.length}`);
      console.log(`   Step sequence: ${stepHistory.map(s => s.step).join(' → ')}`);

      // Chat intent verification
      if (mode === 'auto') {
        const hasWrite = stepHistory.some(s => ['write', 'post_review', 'auto_fix'].includes(s.step));
        if (!hasWrite && payload.article?.length < 500) {
          console.log(`   ✅ Chat intent: writing steps correctly skipped`);
        }
      }

      // Writing mode diagnostics
      if (mode === 'writing') {
        if (fullArticle.length === 0 && fullReasoning.length > 0) {
          console.log('   ❌ BUG: Reasoning streamed but NO article content!');
        } else if (fullArticle.length > 0) {
          console.log('   ✅ Article content streamed successfully');
        }
        if (fullReasoning.length > 0) {
          console.log(`   Reasoning preview: ${fullReasoning.slice(0, 200)}...`);
        }
      }

      console.log(`\n   Article preview: ${(payload.article || fullArticle).slice(0, 200)}...`);
      console.log('═══════════════════════════════════════\n');

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
  console.error('❌ WebSocket error:', err.message);
  process.exit(1);
});

ws.on('close', (code) => {
  if (code === 1000) {
    console.log('\n✅ WebSocket closed normally');
  } else {
    console.log(`\n🔌 WebSocket closed: ${code}`);
  }
});

// Timeout
setTimeout(() => {
  console.error(`\n⏱️  Timeout after ${timeout / 1000}s`);
  console.error(`   reasoning: ${fullReasoning.length} chars (${reasoningChunks} chunks)`);
  console.error(`   article: ${fullArticle.length} chars (${streamChunks} chunks)`);
  ws.close();
  process.exit(1);
}, timeout);
