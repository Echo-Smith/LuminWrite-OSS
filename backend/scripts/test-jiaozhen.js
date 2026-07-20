/**
 * Test: Jiaozhen fact-checking with rumor-triggering article
 * + Stream reset observation
 * + Memory skip for anonymous users
 */
const WebSocket = require('ws');

const WS_URL = 'ws://localhost:8080/api/v2/ws/agent';

// Topic that will produce health claims suitable for Jiaozhen fact-checking
const TEST_MESSAGE = '写一篇关于「吃洋葱能降血压是真的吗」的健康科普文章，要引用一些常见的说法和科学研究';

async function runTest() {
  return new Promise((resolve, reject) => {
    const ws = new WebSocket(WS_URL);
    const startTime = Date.now();

    let streamChunks = 0;
    let reasoningChunks = 0;
    let contentChunks = 0;
    let resetCount = 0;
    let resetDetails = [];
    let steps = [];
    let article = '';
    let articleTitle = '';
    let reviewResult = null;
    let traceId = '';
    let tools = [];

    const timeout = setTimeout(() => {
      ws.close();
      reject(new Error('Timeout after 4min'));
    }, 240000);

    ws.on('open', () => {
      console.log(`Connected, sending: "${TEST_MESSAGE.substring(0, 60)}..."`);
      ws.send(JSON.stringify({
        type: 'agent.start',
        payload: {
          message: TEST_MESSAGE,
          mode: 'writing',
          style: 'yinyue'
        }
      }));
    });

    ws.on('message', (raw) => {
      const msg = JSON.parse(raw.toString());
      const elapsed = ((Date.now() - startTime) / 1000).toFixed(1) + 's';

      switch (msg.type) {
        case 'agent.created':
          traceId = msg.payload?.trace_id || '';
          console.log(`[${elapsed}] Created: ${traceId}`);
          break;

        case 'agent.step.start':
        case 'agent.step.complete': {
          const stepName = msg.payload?.step || msg.payload?.step_name || '';
          const status = msg.type === 'agent.step.start' ? '▶' : '✓';
          steps.push({ name: stepName, type: msg.type, time: elapsed });
          console.log(`[${elapsed}] ${status} ${stepName}`);
          break;
        }

        case 'agent.stream.reset':
          resetCount++;
          console.log(`[${elapsed}] ⚠ STREAM RESET #${resetCount}`);
          resetDetails.push({ time: elapsed, resetCount });
          break;

        case 'agent.stream': {
          streamChunks++;
          if (msg.payload?.reasoning) {
            reasoningChunks++;
          } else {
            contentChunks++;
          }
          break;
        }

        case 'agent.reasoning':
          reasoningChunks++;
          break;

        case 'agent.tool':
        case 'agent.tool_call': {
          const toolName = msg.payload?.name || msg.payload?.tool || '';
          tools.push({ name: toolName, time: elapsed });
          console.log(`[${elapsed}] 🔧 Tool: ${toolName}`);
          break;
        }

        case 'agent.article_title':
          articleTitle = msg.payload?.title || '';
          console.log(`[${elapsed}] 📝 Title: ${articleTitle}`);
          break;

        case 'agent.stream.done':
          article = msg.payload?.text || msg.payload?.content || '';
          console.log(`[${elapsed}] 📄 Article done (${article.length} chars)`);
          break;

        case 'agent.completed':
        case 'agent.complete': {
          clearTimeout(timeout);
          const payload = msg.payload || {};
          article = payload.article || article;
          articleTitle = payload.article_title || articleTitle;
          reviewResult = payload.review_result;

          console.log('\n' + '='.repeat(60));
          console.log('COMPLETE in ' + elapsed);
          console.log('  Trace ID: ' + traceId);
          console.log('  Title: ' + articleTitle);
          console.log('  Article length: ' + article.length + ' chars');
          console.log('  Stream: total=' + streamChunks + ', reasoning=' + reasoningChunks + ', content=' + contentChunks);
          console.log('  Resets: ' + resetCount);
          if (resetDetails.length > 0) {
            console.log('  Reset details:');
            resetDetails.forEach(d => console.log('    - ' + d.time + ' (#' + d.resetCount + ')'));
          }
          console.log('  Tools called: ' + tools.length);
          tools.forEach(t => console.log('    - ' + t.time + ': ' + t.name));
          console.log('  Steps: ' + steps.length);
          steps.forEach(s => console.log('    - ' + s.time + ' ' + (s.type === 'agent.step.start' ? '▶' : '✓') + ' ' + s.name));
          if (reviewResult) {
            console.log('  Review scores: ' + JSON.stringify(reviewResult.scores));
            if (reviewResult.issues && reviewResult.issues.length > 0) {
              console.log('  Review issues:');
              reviewResult.issues.forEach((issue, i) => {
                console.log('    ' + (i+1) + '. [' + issue.dimension + '] ' + issue.description);
              });
            } else {
              console.log('  Review issues: none');
            }
          }

          ws.close();
          resolve({ traceId, article, articleTitle, reviewResult, streamStats: { streamChunks, reasoningChunks, contentChunks, resetCount }, tools, steps });
          break;
        }

        case 'agent.error':
          clearTimeout(timeout);
          console.log('ERROR: ' + JSON.stringify(msg.payload));
          ws.close();
          reject(new Error(JSON.stringify(msg.payload)));
          break;

        default:
          if (!['agent.reasoning', 'agent.stream'].includes(msg.type)) {
            console.log('[' + elapsed + '] ' + msg.type);
          }
      }
    });

    ws.on('error', (err) => {
      clearTimeout(timeout);
      reject(err);
    });
  });
}

runTest().then(() => {
  console.log('\n✅ Test completed');
  process.exit(0);
}).catch((e) => {
  console.log('\n❌ Test failed: ' + e.message);
  process.exit(1);
});
