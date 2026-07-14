// E2E Test: Outline Regeneration + Pause Visual
// Usage: node e2e-regenerate-test.mjs
import WebSocket from 'ws';

const wsUrl = 'ws://localhost:8080/api/v2/ws/agent';
const message = '写一篇关于数字游民生活方式的评论';

console.log(`Connecting to ${wsUrl}...`);
console.log(`Message: ${message}`);
console.log(`Mode: guided\n`);

const ws = new WebSocket(wsUrl);

let traceId = null;
let awaitInputCount = 0;
let firstOutlineTitle = '';
let secondOutlineTitle = '';
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
      awaitInputCount++;
      console.log(`\n⏸️  await_input #${awaitInputCount}: step=${payload.step}`);
      console.log(`   Title: ${payload.data?.title}`);

      if (awaitInputCount === 1) {
        firstOutlineTitle = payload.data?.title;
        console.log(`\n→ Testing REGENERATE... sending action: "regenerate" in 0.5s`);

        setTimeout(() => {
          ws.send(JSON.stringify({
            type: 'agent.confirm',
            payload: {
              trace_id: traceId,
              step: 'outline',
              data: { action: 'regenerate' },
            },
          }));
          console.log('→ agent.confirm (regenerate) sent\n');
        }, 500);
      } else if (awaitInputCount === 2) {
        secondOutlineTitle = payload.data?.title;
        console.log(`\n→ Second outline received. Sending confirm with original data...`);

        const different = firstOutlineTitle !== secondOutlineTitle;
        console.log(`   First title:  ${firstOutlineTitle}`);
        console.log(`   Second title: ${secondOutlineTitle}`);
        console.log(`   Different: ${different ? '✅ YES' : '❌ NO (same outline)'}\n`);

        setTimeout(() => {
          ws.send(JSON.stringify({
            type: 'agent.confirm',
            payload: {
              trace_id: traceId,
              step: 'outline',
              data: payload.data,
            },
          }));
          console.log('→ agent.confirm (confirm) sent\n');
        }, 500);
      }
      break;

    case 'agent.stream':
      if (!articleStarted) {
        console.log('\n📝 Article streaming started:\n---');
        articleStarted = true;
      }
      break;

    case 'agent.stream.done':
      console.log(`\n---\n✅ stream.done (${payload.full_text.length} chars)\n`);
      break;

    case 'agent.paused':
      console.log(`⏸️  agent.paused: step=${payload.step}`);
      break;

    case 'agent.resumed':
      console.log(`▶️  agent.resumed: step=${payload.step}`);
      break;

    case 'agent.completed':
      console.log(`\n🎉 agent.completed`);
      console.log(`   Article length: ${payload.article?.length} chars`);

      console.log('\n═══════════════════════════════════════');
      console.log('📊 Test Summary:');
      console.log(`   ✅ Guided mode await_input received: ${awaitInputCount >= 1 ? 'PASS' : 'FAIL'}`);
      console.log(`   ✅ Regeneration triggered (2nd await_input): ${awaitInputCount >= 2 ? 'PASS' : 'FAIL'}`);
      console.log(`   ${firstOutlineTitle !== secondOutlineTitle ? '✅' : '⚠️'}  Outlines are different: ${firstOutlineTitle !== secondOutlineTitle ? 'YES' : 'NO (may be same due to LLM randomness)'}`);
      console.log(`   ✅ Article generated: ${payload.article?.length > 0 ? 'PASS' : 'FAIL'}`);
      console.log('═══════════════════════════════════════\n');

      ws.close();
      process.exit(0);
      break;

    case 'agent.error':
      console.log(`\n❌ agent.error: ${payload.code} - ${payload.message}`);
      ws.close();
      process.exit(1);
      break;
  }
});

ws.on('error', (e) => {
  console.error('WebSocket error:', e);
});

setTimeout(() => {
  console.log('\n⏱️  Test timed out after 120s');
  ws.close();
  process.exit(1);
}, 120000);
