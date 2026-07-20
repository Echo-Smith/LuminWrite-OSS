const WebSocket = require('ws');
const ws = new WebSocket('ws://localhost:8080/api/v2/ws/agent');

ws.on('open', () => {
  console.log('Connected');
  ws.send(JSON.stringify({
    type: 'agent.start',
    payload: {
      message: '基于热搜选题「人工智能教育应用」写一篇评论文章',
      mode: 'writing',
      style: 'yinyue'
    }
  }));
});

let streamChunks = 0;
let resetCount = 0;
let reasoningChunks = 0;
let contentChunks = 0;

ws.on('message', (raw) => {
  const msg = JSON.parse(raw.toString());
  const ts = new Date().toISOString().substring(11,19);

  if (msg.type === 'agent.step') {
    console.log(`[${ts}] STEP: ${msg.payload.step} ${msg.payload.status || ''}`);
  } else if (msg.type === 'agent.stream.reset') {
    resetCount++;
    console.log(`[${ts}] STREAM RESET (#${resetCount})`);
  } else if (msg.type === 'agent.stream') {
    streamChunks++;
    if (msg.payload.reasoning) {
      reasoningChunks++;
      if (reasoningChunks % 50 === 0) process.stdout.write('R');
    } else {
      contentChunks++;
      if (contentChunks % 50 === 0) process.stdout.write('C');
    }
  } else if (msg.type === 'agent.tool') {
    console.log(`[${ts}] TOOL: ${msg.payload.name || msg.payload.tool || ''}`);
  } else if (msg.type === 'agent.search') {
    console.log(`[${ts}] SEARCH: ${JSON.stringify(msg.payload).substring(0, 100)}`);
  } else if (msg.type === 'agent.complete') {
    console.log('\n' + '='.repeat(60));
    console.log('COMPLETE:', msg.payload.trace_id);
    console.log('Article length:', (msg.payload.article || '').length);
    console.log('Stream: total=' + streamChunks + ' reasoning=' + reasoningChunks + ' content=' + contentChunks + ' resets=' + resetCount);
    if (msg.payload.review_result) {
      console.log('Review scores:', JSON.stringify(msg.payload.review_result.scores));
      if (msg.payload.review_result.issues && msg.payload.review_result.issues.length > 0) {
        console.log('Review issues:');
        msg.payload.review_result.issues.forEach((issue, i) => {
          console.log(`  ${i+1}. [${issue.dimension}] ${issue.description}`);
        });
      }
    }
    ws.close();
    process.exit(0);
  } else if (msg.type === 'agent.error') {
    console.log('ERROR:', JSON.stringify(msg.payload));
    ws.close();
    process.exit(1);
  } else if (msg.type === 'agent.thinking') {
    // skip
  } else {
    console.log(`[${ts}] ${msg.type}`);
  }
});

ws.on('error', (err) => {
  console.error('WS Error:', err.message);
  process.exit(1);
});

setTimeout(() => { console.log('\nTimeout after 3min'); process.exit(1); }, 180000);
