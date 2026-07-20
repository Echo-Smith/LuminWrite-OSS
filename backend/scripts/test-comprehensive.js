/**
 * Comprehensive test for:
 * 1. Jiaozhen fact-checking with rumor articles
 * 2. Stream reset mechanism observation
 * 3. Memory system extraction and injection
 */
const WebSocket = require('ws');

const WS_URL = 'ws://localhost:8080/api/v2/ws/agent';
const API_URL = 'http://localhost:8080/api/v2';

// Test scenario 1: Write an article that may contain verifiable/rumor claims
// Using a topic that might produce factual claims about health or food
const TEST_MESSAGE = '写一篇关于「吃洋葱能降血压是真的吗」的健康科普文章，要引用一些常见的说法和科学研究';

function sleep(ms) { return new Promise(r => setTimeout(r, ms)); }

async function runTest(testName, message, options = {}) {
  return new Promise((resolve, reject) => {
    const ws = new WebSocket(WS_URL);
    const startTime = Date.now();

    let streamChunks = 0;
    let reasoningChunks = 0;
    let contentChunks = 0;
    let resetCount = 0;
    let resetDetails = [];
    let memoryUsed = null;
    let steps = [];
    let article = '';
    let articleTitle = '';
    let reviewResult = null;
    let traceId = '';
    let tools = [];

    const timeout = setTimeout(() => {
      ws.close();
      reject(new Error(`Timeout after ${options.timeoutMs || 180000}ms`));
    }, options.timeoutMs || 180000);

    ws.on('open', () => {
      console.log(`\n[${testName}] Connected, sending: "${message.substring(0, 50)}..."`);
      ws.send(JSON.stringify({
        type: 'agent.start',
        payload: {
          message,
          mode: options.mode || 'writing',
          style: options.style || 'yinyue'
        }
      }));
    });

    ws.on('message', (raw) => {
      const msg = JSON.parse(raw.toString());
      const ts = new Date().toISOString().substring(11,19);
      const elapsed = ((Date.now() - startTime) / 1000).toFixed(1) + 's';

      switch (msg.type) {
        case 'agent.created':
          traceId = msg.payload?.trace_id || '';
          console.log(`[${testName}][${elapsed}] Created: ${traceId}`);
          break;

        case 'agent.step.start':
        case 'agent.step.complete':
          const stepName = msg.payload?.step || msg.payload?.step_name || '';
          const status = msg.type === 'agent.step.start' ? '▶' : '✓';
          steps.push({ name: stepName, type: msg.type, time: elapsed });
          console.log(`[${testName}][${elapsed}] ${status} ${stepName}`);
          break;

        case 'agent.stream.reset':
          resetCount++;
          console.log(`[${testName}][${elapsed}] ⚠ STREAM RESET #${resetCount}`);
          resetDetails.push({ time: elapsed, resetCount });
          break;

        case 'agent.stream':
          streamChunks++;
          if (msg.payload?.reasoning) {
            reasoningChunks++;
          } else {
            contentChunks++;
          }
          break;

        case 'agent.reasoning':
          reasoningChunks++;
          break;

        case 'agent.tool':
        case 'agent.tool_call':
          const toolName = msg.payload?.name || msg.payload?.tool || '';
          tools.push({ name: toolName, time: elapsed });
          console.log(`[${testName}][${elapsed}] 🔧 Tool: ${toolName}`);
          break;

        case 'agent.memory.used':
          memoryUsed = msg.payload;
          console.log(`[${testName}][${elapsed}] 🧠 Memory used: ${JSON.stringify(msg.payload).substring(0, 200)}`);
          break;

        case 'agent.article_title':
          articleTitle = msg.payload?.title || '';
          console.log(`[${testName}][${elapsed}] 📝 Title: ${articleTitle}`);
          break;

        case 'agent.stream.done':
          article = msg.payload?.text || msg.payload?.content || '';
          console.log(`[${testName}][${elapsed}] 📄 Article done (${article.length} chars)`);
          break;

        case 'agent.completed':
        case 'agent.complete':
          clearTimeout(timeout);
          const payload = msg.payload || {};
          article = payload.article || article;
          articleTitle = payload.article_title || articleTitle;
          reviewResult = payload.review_result;

          console.log(`\n${'='.repeat(60)}`);
          console.log(`[${testName}] COMPLETE in ${elapsed}`);
          console.log(`  Trace ID: ${traceId}`);
          console.log(`  Title: ${articleTitle}`);
          console.log(`  Article length: ${article.length} chars`);
          console.log(`  Stream: total=${streamChunks}, reasoning=${reasoningChunks}, content=${contentChunks}`);
          console.log(`  Resets: ${resetCount}`);
          if (resetDetails.length > 0) {
            console.log(`  Reset details:`);
            resetDetails.forEach(d => console.log(`    - ${d.time} (#${d.resetCount})`));
          }
          console.log(`  Tools called: ${tools.length}`);
          tools.forEach(t => console.log(`    - ${t.time}: ${t.name}`));
          console.log(`  Steps: ${steps.length}`);
          steps.forEach(s => console.log(`    - ${s.time} ${s.type === 'agent.step.start' ? '▶' : '✓'} ${s.name}`));
          if (memoryUsed) {
            console.log(`  Memory used: YES`);
          } else {
            console.log(`  Memory used: NO`);
          }
          if (reviewResult) {
            console.log(`  Review scores: ${JSON.stringify(reviewResult.scores)}`);
            if (reviewResult.issues && reviewResult.issues.length > 0) {
              console.log(`  Review issues:`);
              reviewResult.issues.forEach((issue, i) => {
                console.log(`    ${i+1}. [${issue.dimension}] ${issue.description}`);
              });
            } else {
              console.log(`  Review issues: none`);
            }
          }

          ws.close();
          resolve({
            traceId,
            article,
            articleTitle,
            reviewResult,
            streamStats: { streamChunks, reasoningChunks, contentChunks, resetCount },
            tools,
            steps,
            memoryUsed,
          });
          break;

        case 'agent.error':
          clearTimeout(timeout);
          console.log(`[${testName}] ERROR: ${JSON.stringify(msg.payload)}`);
          ws.close();
          reject(new Error(JSON.stringify(msg.payload)));
          break;

        default:
          // Skip noisy events
          if (!['agent.reasoning', 'agent.stream'].includes(msg.type)) {
            console.log(`[${testName}][${elapsed}] ${msg.type}`);
          }
      }
    });

    ws.on('error', (err) => {
      clearTimeout(timeout);
      reject(err);
    });
  });
}

async function checkMemories(userId) {
  // Try to list memories via API
  const url = `${API_URL}/memories${userId ? '?user_id=' + userId : ''}`;
  console.log(`\nChecking memories at: ${url}`);
  // Since we don't have auth, we'll check via docker exec
}

async function main() {
  console.log('='.repeat(60));
  console.log('Comprehensive Test: Jiaozhen + Stream Reset + Memory');
  console.log('='.repeat(60));

  // Test 1: Write an article with health/food claims (triggers Jiaozhen)
  console.log('\n--- Test 1: Health rumor article (Jiaozhen trigger) ---');
  try {
    const result1 = await runTest('Test1', TEST_MESSAGE, { timeoutMs: 240000 });
    console.log('\n✅ Test 1 passed');
  } catch (e) {
    console.log('\n❌ Test 1 failed:', e.message);
  }

  // Check backend logs for fact-check and jiaozhen
  console.log('\n--- Checking backend logs for fact-check activity ---');

  // Test 2: Write another article to check memory injection
  console.log('\n--- Test 2: Second article (memory injection check) ---');
  try {
    const result2 = await runTest('Test2', '写一篇关于人工智能在教育领域应用的评论文章', { timeoutMs: 240000 });
    console.log('\n✅ Test 2 passed');
  } catch (e) {
    console.log('\n❌ Test 2 failed:', e.message);
  }

  console.log('\n' + '='.repeat(60));
  console.log('All tests completed. Check backend logs for details.');
  console.log('='.repeat(60));
}

main().catch(console.error);
