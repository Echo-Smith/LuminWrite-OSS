// Guided Mode E2E Test for Writing Agent V2
// Tests: outline confirm, outline regenerate, pause/resume during streaming
// Usage:
//   node e2e-guided-test.mjs                       # default: confirm flow + pause/resume
//   node e2e-guided-test.mjs --regenerate           # test outline regeneration
//   node e2e-guided-test.mjs "你的消息"             # custom message
//   node e2e-guided-test.mjs --regenerate "数字游民" # regenerate + custom message
import WebSocket from 'ws';

// ── Parse args ──────────────────────────────────────
const args = process.argv.slice(2);
const testRegenerate = args.includes('--regenerate');
const message = args.find(a => !a.startsWith('--')) || '基于热搜写一篇关于外卖骑手闯红灯的评论';

const wsUrl = process.env.WS_URL || 'ws://localhost:8080/api/v2/ws/agent';

console.log(`Connecting to ${wsUrl}...`);
console.log(`Message: ${message}`);
console.log(`Mode: guided | Test: ${testRegenerate ? 'regenerate' : 'confirm + pause/resume'}\n`);

// ── State ───────────────────────────────────────────
const ws = new WebSocket(wsUrl);
let traceId = null;
let streamChunks = 0;
let fullArticle = '';
let awaitInputCount = 0;
let firstOutlineTitle = '';
let secondOutlineTitle = '';
let pausedDuringStream = false;
let articleStarted = false;

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
      {
        awaitInputCount++;
        console.log(`\n⏸️  await_input #${awaitInputCount}: step=${payload.step}`);
        console.log(`   Title: ${payload.data?.title}`);
        console.log(`   Outline points: ${payload.data?.outline?.length || 0}`);
        payload.data?.outline?.forEach((item, i) => {
          console.log(`   ${i + 1}. [${item.type}] ${item.point}`);
        });

        if (testRegenerate && awaitInputCount === 1) {
          // ── Regenerate test ──────────────────────────
          firstOutlineTitle = payload.data?.title;
          console.log(`\n→ Testing REGENERATE... sending action: "regenerate" in 0.5s`);

          setTimeout(() => {
            ws.send(JSON.stringify({
              type: 'agent.confirm',
              payload: { trace_id: traceId, step: 'outline', data: { action: 'regenerate' } },
            }));
            console.log('→ agent.confirm (regenerate) sent\n');
          }, 500);
        } else if (testRegenerate && awaitInputCount === 2) {
          // ── Confirm after regenerate ─────────────────
          secondOutlineTitle = payload.data?.title;
          const different = firstOutlineTitle !== secondOutlineTitle;
          console.log(`\n→ Second outline received.`);
          console.log(`   First title:  ${firstOutlineTitle}`);
          console.log(`   Second title: ${secondOutlineTitle}`);
          console.log(`   Different: ${different ? '✅ YES' : '⚠️  NO (LLM randomness)'}\n`);

          setTimeout(() => {
            ws.send(JSON.stringify({
              type: 'agent.confirm',
              payload: { trace_id: traceId, step: 'outline', data: payload.data },
            }));
            console.log('→ agent.confirm sent\n');
          }, 500);
        } else {
          // ── Normal confirm ───────────────────────────
          console.log(`\n→ Sending confirm with original data in 1s...`);

          setTimeout(() => {
            ws.send(JSON.stringify({
              type: 'agent.confirm',
              payload: { trace_id: traceId, step: 'outline', data: payload.data },
            }));
            console.log('→ agent.confirm sent\n');
          }, 1000);
        }
      }
      break;

    case 'agent.stream':
      if (!articleStarted) {
        console.log('\n📝 Article streaming:\n---');
        articleStarted = true;
      }
      process.stdout.write(payload.delta);
      fullArticle += payload.delta;
      streamChunks++;

      // Test pause/resume after 10 chunks (only in non-regenerate mode)
      if (streamChunks === 10 && !pausedDuringStream && !testRegenerate) {
        pausedDuringStream = true;
        console.log('\n\n⏸️  Testing pause during streaming...');
        ws.send(JSON.stringify({
          type: 'agent.pause',
          payload: { trace_id: traceId },
        }));
        console.log('→ agent.pause sent\n');

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
      console.log(`\n---\n✅ stream.done (${streamChunks} chunks, ${payload.full_text?.length || 0} chars)\n`);
      break;

    case 'agent.completed':
      console.log(`\n🎉 agent.completed`);
      console.log(`   Article length: ${payload.article?.length || 0} chars`);
      console.log(`   Review passed: ${payload.review?.passed}`);
      console.log(`   Token usage:`, payload.token_usage);

      // ── Summary ───────────────────────────────────
      console.log('\n═══════════════════════════════════════');
      console.log('📊 Test Summary:');

      if (testRegenerate) {
        console.log(`   ${awaitInputCount >= 2 ? '✅' : '❌'} Regeneration triggered (2nd await_input): ${awaitInputCount >= 2 ? 'PASS' : 'FAIL'}`);
        console.log(`   ${firstOutlineTitle !== secondOutlineTitle ? '✅' : '⚠️ '}  Outlines different: ${firstOutlineTitle !== secondOutlineTitle ? 'YES' : 'NO'}`);
      } else {
        console.log(`   ${awaitInputCount >= 1 ? '✅' : '❌'} Guided mode await_input: ${awaitInputCount >= 1 ? 'PASS' : 'FAIL'}`);
        console.log(`   ${pausedDuringStream ? '✅' : '⏭️ '} Pause/Resume during stream: ${pausedDuringStream ? 'TESTED' : 'SKIPPED'}`);
      }

      console.log(`   ${fullArticle.length > 0 ? '✅' : '❌'} Article generated: ${fullArticle.length > 0 ? 'PASS' : 'FAIL'}`);
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

// Timeout after 120s
setTimeout(() => {
  console.error('\n⏱️  Test timed out after 120s');
  ws.close();
  process.exit(1);
}, 120000);
