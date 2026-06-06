# E2E Benchmark Framework

This document covers the end-to-end benchmark runner in DeepSeek-Reasonix, which evaluates agent performance against a real provider and produces structured reports for accuracy, cache hit rates, token usage, and cost.

**Source:** `cmd/e2ebench/main.go`, `cmd/e2ebench/diff.go`, `cmd/e2ebench/mutation.go`

---

## Overview

The e2ebench tool runs the Reasonix agent against real LLM providers with well-defined tasks, then grades the results using verification scripts. It operates in two modes: **suite** mode for committed benchmark tasks, and **diff** mode for generating tests from a PR's diff. The output is a markdown report (suitable for posting as a PR comment) and an optional JSON report for programmatic consumption.

---

## Two Modes of Operation

### Suite Mode

Suite mode runs a set of committed task files from a directory hierarchy. Each task lives in its own subdirectory under `benchmarks/e2e/tasks/`, containing:

- `task.toml` — Task definition (prompt, limits)
- `workdir/` (optional) — Seed directory copied to a temp location before the run
- `verify.sh` — Grading script that runs after the agent completes

This mode is designed for regression testing: the same set of tasks is run periodically to track accuracy and cost over time. The committed tasks serve as a stable baseline that shouldn't change frequently.

### Diff Mode

Diff mode generates tests from a pull request's diff and grades them against the repository's own test suite. It is designed for CI/CD integration: when a PR changes Go source files, the agent is asked to write unit tests covering the new or changed behavior, and those tests are evaluated for quality using multiple signals (pass/fail, differential analysis, mutation testing, changed-line coverage).

---

## Task Definition (Suite Mode)

Each task is defined by a TOML file with the following fields:

| Field | Type | Description |
|-------|------|-------------|
| `prompt` | string | The instruction sent to the agent |
| `max_steps` | int | Maximum number of tool-call turns the agent may take |
| `timeout_sec` | int | Wall-clock timeout in seconds (default: 240) |
| `workdir/` | directory | Seed files copied to the agent's working directory before the run |
| `verify.sh` | script | Grading script run after the agent completes |

### Example Task

```toml
# benchmarks/e2e/tasks/fix-add-bug/task.toml
prompt = "Fix the add function in calc.py which currently returns incorrect results for negative numbers"
max_steps = 20
timeout_sec = 300
```

The corresponding `workdir/calc.py` would contain the buggy code, and `verify.sh` would test the fix.

---

## runTask Execution Model

The `runTask` function executes a single benchmark task:

1. **Create temp directory**: A fresh temporary directory is created under `/tmp/e2ebench-{task-id}-*/`
2. **Copy seed workdir**: If the task has a `workdir/` subdirectory, its contents are recursively copied to the temp directory using `copyDir`
3. **Run the agent**: The Reasonix binary is invoked with the task's prompt, model, max-steps, and a `--metrics` flag that writes token usage to a JSON file in the working directory
4. **Copy verification script**: The `verify.sh` from the task directory is copied into the working directory **after** the agent run completes. This is critical: if the grader were present during the run, the agent could read it and learn the expected answers.
5. **Grade the result**: `verify.sh` is executed via `bash verify.sh` in the working directory. A zero exit code means the task passed.
6. **Collect metrics**: Token counts, cache statistics, cost, and compaction count are read from the `.run-metrics.json` file.

### File Copy Semantics

The `copyDir` function preserves file permissions (so executable seed scripts stay executable) and **skips symlinks** entirely, preventing a seed link from leaking a file from outside the seed tree. The `copyFile` function mirrors the source file's mode via `os.Chmod` after the copy, ensuring that read-only and executable bits survive.

---

## Metrics Collection

Each task run produces a `runMetrics` struct:

| Field | JSON Key | Description |
|-------|----------|-------------|
| PromptTokens | `prompt_tokens` | Total prompt tokens consumed |
| CompletionTokens | `completion_tokens` | Total completion tokens generated |
| CacheHitTokens | `cache_hit_tokens` | Prompt tokens served from cache |
| CacheMissTokens | `cache_miss_tokens` | Prompt tokens not served from cache |
| Steps | `steps` | Number of model API calls |
| Cost | `cost` | Estimated cost in the reported currency |
| Currency | `currency` | ISO currency code (e.g., "USD", "CNY") |
| Compactions | `compactions` | Number of context compaction operations |

The cache hit percentage is computed as `CacheHitTokens / (CacheHitTokens + CacheMissTokens)` and reported in the markdown table.

---

## Budget Cap

The `-budget` flag (default: 400,000 tokens) sets a total token budget across all tasks. Once the cumulative token count (prompt + completion) crosses this threshold, remaining tasks are skipped with a "skipped: token budget reached" note. This prevents runaway costs during benchmark runs, especially when testing against expensive models.

A budget of 0 disables the cap entirely, useful for small test suites or when cost is not a concern.

---

## Markdown Report

The `render` function produces a markdown report with:

1. **Summary line**: Accuracy (pass/run), cache hit %, total tokens (prompt/completion split), compaction count, and total cost
2. **Per-task table**: Each row shows task ID, result (pass/fail/skipped), steps, prompt tokens, completion tokens, cache hit %, compactions, and cost
3. **Notes section**: Collapsible `<details>` block listing any task-specific notes (run errors, timeout messages)

### Example Output

```markdown
## 🤖 Reasonix e2e benchmark

**Accuracy:** 3/4 (75%) · **Cache hit:** 62% · **Tokens:** 45,230 (prompt 38,100 / completion 7,130) · **Compactions:** 1 · **Cost:** USD 0.0342

| Task | Result | Steps | Prompt | Completion | Cache hit | Compact | Cost |
|------|--------|------:|-------:|-----------:|----------:|--------:|-----:|
| `fix-add-bug` | ✅ pass | 8 | 12,400 | 2,100 | 71% | 0 | USD 0.0091 |
| `fizzbuzz` | ✅ pass | 5 | 8,200 | 1,400 | 65% | 0 | USD 0.0058 |
| `palindrome` | ❌ fail | 20 | 15,700 | 3,600 | 54% | 1 | USD 0.0149 |
| `compaction` | ✅ pass | 12 | 11,800 | 2,030 | 58% | 0 | USD 0.0044 |

<sub>Real provider run. Cache-hit % is cached prompt tokens / total prompt tokens.</sub>
```

---

## Diff Mode

Diff mode is the more sophisticated evaluation path, designed for CI/CD integration with pull requests.

### Pipeline

1. **Identify changed files**: `git diff --name-only {base}...HEAD -- *.go` lists Go source files changed by the PR (excluding `_test.go` files)
2. **Build the prompt**: The agent receives the list of changed files and the full unified diff, with instructions to write focused unit tests covering the new or changed behavior
3. **Run the agent**: The Reasonix binary is invoked with the diff prompt, max-steps, and timeout
4. **Stage agent output**: `git add -AN` surfaces the agent's new test files in the diff
5. **Grade on HEAD**: Run `go test` on the affected packages to verify the new tests pass
6. **Differential analysis**: Revert the PR's source changes and re-run each new test individually. A test that fails on the pre-PR code "pins" the change.
7. **Mutation testing**: For each changed function, replace its return values with zero values and re-run the tests. Tests that fail catch the mutation; those that don't are "survivors."
8. **Changed-line coverage**: Run `go test -coverprofile` and measure how many of the PR's changed lines the new tests actually execute.
9. **Build regression check**: Run `go build ./...` to ensure the agent didn't break anything outside the test files.

### Retry Budget

Because the agent is stochastic, diff mode supports a retry budget via the `-attempts` flag. The runner executes up to N attempts and keeps the best result. The `better` function ranks results by:

1. Pass/fail (a pass always beats a fail)
2. More assertion pins (tests that check changed behavior by assertion)
3. More total pins (tests that fail on old code, even if only by compile error)
4. More mutation kills (caught > survived)
5. Higher changed-line coverage

Between attempts, `resetTree` restores the PR-head tree by running `git checkout -- .` and `git clean -fd -e reasonix.toml` (preserving the provider config that the workflow may have written).

### Differential Analysis

The `differentialPerTest` function is the core quality signal. It reverts each changed source file to the base commit, runs each new test function individually, and records whether it fails:

- **Pins by assertion**: The test fails with `--- FAIL: TestName` — it explicitly checks the new behavior. This is the strongest signal.
- **Pins by compile only**: The test fails to compile because it references a symbol the PR added. This is weaker but still indicates the test depends on the PR's changes.
- **No pin**: The test passes on both old and new code — it doesn't exercise any changed behavior.

The function uses a `defer` to ensure the source tree is restored even if a panic occurs during testing, preventing the CI environment from being left in a broken state.

### Pass Criteria

A diff-mode run passes when **all** of the following are true:

1. At least one new test function was added
2. The affected packages' tests pass on HEAD
3. The build succeeds (`go build ./...`)
4. At least one new test pins the change (fails when the PR's source is reverted)

This is a high bar: the agent must not only write passing tests, but those tests must actually exercise the changed behavior in a way that would catch a regression.

---

## JSON Report

The optional `-json` flag writes the full results array as pretty-printed JSON, suitable for ingestion by CI dashboards or trend analysis tools. Each result includes the task ID, all run metrics, pass/fail status, and any notes. The JSON schema is stable across versions, enabling long-term tracking of benchmark trends.
