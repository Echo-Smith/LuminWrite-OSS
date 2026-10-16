import { runDemo } from "./run-demo.js";

const result = await runDemo();
process.stdout.write(`${result.log}\n`);
if (!result.ok) process.exit(1);
