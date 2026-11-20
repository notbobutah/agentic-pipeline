// ap-runner: runs one agent task inside the sandbox with the Claude Agent SDK.
//
// Contract with the Go gateway (internal/agent):
//   in   /work/task.json   role, model, prompts, tools, cwd, budgets, output schema
//   out  stdout (JSONL)    progress lines: {"kind":"tool"|"text"|"status", ...}
//   out  /work/result.json { subtype, isError, structured, text, costUsd, turns,
//                            durationMs, permissionDenials, errors }
//
// The runner holds no GitHub credentials and never pushes; the host applies
// any changes after its scope checks (docs/14 §6, ADR-0005).
import { query, type SDKResultMessage } from "@anthropic-ai/claude-agent-sdk";
import { readFile, writeFile } from "node:fs/promises";
import { dirname, join } from "node:path";

interface Task {
  role: string;
  model: string;
  systemPrompt: string;
  prompt: string;
  tools: string[];
  cwd: string;
  maxTurns: number;
  maxBudgetUsd: number;
  outputSchema?: Record<string, unknown>;
}

interface Result {
  subtype: string;
  isError: boolean;
  structured: unknown;
  text: string;
  costUsd: number;
  turns: number;
  durationMs: number;
  permissionDenials: number;
  errors: string[];
}

const taskPath = process.argv[2] ?? "/work/task.json";
const resultPath = join(dirname(taskPath), "result.json");
const progress = (o: Record<string, unknown>) => process.stdout.write(JSON.stringify(o) + "\n");

async function main(): Promise<number> {
  const task = JSON.parse(await readFile(taskPath, "utf8")) as Task;
  const started = Date.now();
  progress({ kind: "status", status: "started", role: task.role, model: task.model });

  let final: SDKResultMessage | undefined;
  try {
    for await (const msg of query({
      prompt: task.prompt,
      options: {
        model: task.model,
        systemPrompt: task.systemPrompt,
        cwd: task.cwd,
        // `tools` limits what exists; `allowedTools` pre-approves it. With
        // permissionMode "dontAsk" anything else is denied, never prompted.
        tools: task.tools,
        allowedTools: task.tools,
        permissionMode: "dontAsk",
        maxTurns: task.maxTurns,
        maxBudgetUsd: task.maxBudgetUsd,
        settingSources: [],
        persistSession: false,
        outputFormat: task.outputSchema ? { type: "json_schema", schema: task.outputSchema } : undefined,
        env: { ...process.env, CLAUDE_AGENT_SDK_CLIENT_APP: "agentic-pipeline/0.1" },
      },
    })) {
      if (msg.type === "assistant") {
        for (const block of msg.message.content) {
          if (block.type === "tool_use") progress({ kind: "tool", name: block.name });
          else if (block.type === "text" && block.text.trim()) progress({ kind: "text", text: block.text.slice(0, 400) });
        }
      } else if (msg.type === "result") {
        final = msg;
      }
    }
  } catch (err) {
    await write({ subtype: "error_during_execution", isError: true, errors: [String(err)], durationMs: Date.now() - started });
    progress({ kind: "status", status: "failed", error: String(err) });
    return 1;
  }

  if (!final) {
    await write({ subtype: "error_during_execution", isError: true, errors: ["no result message"], durationMs: Date.now() - started });
    return 1;
  }
  const base = {
    costUsd: final.total_cost_usd,
    turns: final.num_turns,
    durationMs: final.duration_ms,
    permissionDenials: final.permission_denials.length,
  };
  if (final.subtype === "success") {
    await write({ ...base, subtype: final.subtype, isError: final.is_error, structured: final.structured_output ?? null, text: final.result });
  } else {
    await write({ ...base, subtype: final.subtype, isError: true, errors: final.errors });
  }
  progress({ kind: "status", status: "done", subtype: final.subtype, costUsd: base.costUsd, turns: base.turns });
  return 0;
}

async function write(r: Partial<Result>): Promise<void> {
  const full: Result = {
    subtype: "error_during_execution", isError: true, structured: null, text: "",
    costUsd: 0, turns: 0, durationMs: 0, permissionDenials: 0, errors: [], ...r,
  };
  await writeFile(resultPath, JSON.stringify(full, null, 2));
}

process.exitCode = await main();
