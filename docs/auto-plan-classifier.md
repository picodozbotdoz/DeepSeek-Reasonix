# Auto-Plan Classifier

This document covers the two-stage auto-plan system that determines whether a user's request warrants entering planning mode before execution. The system combines a fast heuristic scorer with an optional LLM-based classifier for ambiguous cases.

**Source:** `internal/control/auto_plan.go`, `internal/control/auto_plan_classifier.go`

---

## Overview

When a user submits a complex, multi-step request (e.g., "Implement user authentication with JWT tokens across the backend and frontend"), it's beneficial for the agent to first draft a plan and seek approval before making changes. Conversely, simple questions like "What does this function do?" should proceed directly without the overhead of planning.

The auto-plan system automatically detects when planning is warranted by evaluating the user's input through two stages:

1. **Heuristic scorer** (`autoPlanScore`): A fast, zero-cost function that counts signal indicators in the input text.
2. **LLM classifier** (`ProviderAutoPlanClassifier`): An optional, lightweight LLM call that resolves ambiguous cases where the heuristic score is between 1 and 2.

---

## Heuristic Scorer: autoPlanScore

`autoPlanScore(input)` evaluates the user's input on seven orthogonal signals, each contributing +1 to the total score:

### Signal 1: Length ≥ 160 Runes

Long inputs are more likely to describe complex tasks. The threshold of 160 runes (not bytes) was chosen to be CJK-aware — a Chinese character counts as one rune, so a request like "请实现用户认证功能，包括JWT token的生成和验证，以及前端的登录页面和后端的API接口" easily exceeds this threshold.

### Signal 2: Numbered/Bulleted List Detected

The regular expression `(?m)^\s*(?:[-*]|\d+[.)])\s+\S` detects Markdown-style list items at the start of lines. A request that enumerates steps or requirements (e.g., "1. Add login page\n2. Implement JWT\n3. Write tests") signals a structured, multi-step task.

### Signal 3: Newline Count ≥ 2

Inputs with multiple line breaks are likely describing multi-part requirements. A single-line query rarely warrants a plan; a multi-paragraph specification often does.

### Signal 4: Complex Intent Terms

The input is checked against a list of terms that indicate substantial implementation work:

**English terms:** `implement`, `add support`, `refactor`, `migrate`, `redesign`, `end-to-end`, `e2e`, `wire up`, `integration`, `fix the issue`, `build a`

**Chinese terms:** `实现`, `新增`, `支持`, `重构`, `迁移`, `改造`, `端到端`, `联调`, `接入`, `修复这个问题`, `修一下这个问题`, `补齐`, `设计`

These terms were chosen to capture the difference between "explain X" (informational) and "implement X" (work request).

### Signal 5: Multi-Surface Terms

The input is checked for terms indicating work that spans multiple areas of a codebase:

**English terms:** `multiple files`, `several files`, `across`, `frontend`, `backend`, `config`, `tests`, `docs`, `ui`, `api`, `database`, `schema`

**Chinese terms:** `多个文件`, `多处`, `前端`, `后端`, `配置`, `测试`, `文档`, `接口`, `数据库`

When a request mentions both "frontend" and "backend" or talks about "multiple files," it's likely a cross-cutting task that benefits from planning.

### Signal 6: Docs/Issue Terms

The input is checked for terms that reference formal specifications:

**English terms:** `prd`, `issue`, `requirements`, `spec`, `proposal`, `roadmap`

**Chinese terms:** `需求`, `产品文档`, `接口文档`, `方案`, `规划`

Requests that reference PRDs, specs, or issues typically describe scoped, multi-step work that should be planned.

### Signal 7: Multiple @references or 2+ File Extensions

The input is checked for:
- Two or more `@` characters (indicating multiple file or resource references).
- Two or more occurrences of common file extensions (`.go`, `.ts`, `.tsx`, `.js`).

Referencing multiple files or file types suggests the task touches multiple parts of the codebase.

---

## Low-Risk Question Detection: isLowRiskQuestion

`isLowRiskQuestion(lower)` detects informational queries that should skip planning entirely. It checks if the lowercased input starts with specific prefixes:

**English prefixes:** `run `, `show `, `what `, `why `, `how `

**Chinese prefixes:** `解释`, `说明`, `怎么看`, `查一下`, `运行`

**Important override**: Even if the input starts with a low-risk prefix, it is **not** classified as low-risk if it also contains complex intent terms. For example, "how to implement authentication" starts with "how" but contains "implement," so it's not considered low-risk. This prevents the classifier from accidentally skipping planning for genuinely complex requests that happen to start with an informational word.

When `isLowRiskQuestion` returns true, the auto-plan score is forced to 0, regardless of other signals.

---

## Decision Logic: shouldAutoPlan

`shouldAutoPlan(ctx, input)` combines the heuristic score with the optional LLM classifier:

1. **Check preconditions**: If auto-plan mode is `"off"`, plan mode is already active, or bypass/YOLO mode is enabled, return false. (YOLO mode means "don't stop to ask" — entering plan mode would gate on approval, the opposite of what the user intended.)

2. **Compute score**: `autoPlanScore(input)`.

3. **Score ≤ 0**: No plan. The input is either empty, a slash command, or a low-risk question.

4. **Score ≥ 2 (without classifier)**: Plan. The heuristic is confident enough on its own.

5. **Score 1–2 with classifier available**: Defer to the LLM classifier with a 3-second timeout. The classifier returns `needs_plan` (boolean) and `reason` (string). If the classifier succeeds, its verdict is used. If it fails (timeout, network error), the system falls back to the heuristic: score ≥ 2 means plan.

6. **Score 1 without classifier**: No plan. One signal alone isn't enough to warrant planning without corroboration.

### Bypass and YOLO Mode

When bypass/YOLO mode is active, the function always returns false. This is intentional: YOLO mode means "don't stop to ask," and entering plan mode would draft a plan and gate on approval, which contradicts the user's explicit choice to skip confirmations.

---

## LLM Classifier: ProviderAutoPlanClassifier

`ProviderAutoPlanClassifier` uses a configured LLM provider to classify whether a request needs planning. It is only invoked for ambiguous cases (heuristic score 1–2).

### Construction

`NewProviderAutoPlanClassifier(prov)` wraps a `provider.Provider`. If the provider is nil, the constructor returns nil, and the classifier is treated as unavailable (the heuristic decides alone).

### Classification Prompt

The classifier uses a concise system prompt:

```
You classify whether a coding-agent user request should first enter read-only planning mode.
Return ONLY JSON: {"needs_plan":true|false,"reason":"short reason"}.
Use true for multi-step implementation, refactors, migrations, unclear cross-file work, PRD/spec/issue work, or tasks needing investigation before edits.
Use false for explanations, simple questions, single obvious edits, direct commands, or requests that should be answered without changing files.
```

The user message includes both the heuristic score and the raw input:

```
heuristic_score=2

USER_REQUEST:
Refactor the authentication module to support both JWT and session-based auth
```

### Classification Parameters

| Parameter | Value | Rationale |
|---|---|---|
| `Temperature` | `0` | Deterministic classification; no creativity needed. |
| `MaxTokens` | `80` | The response is a short JSON object; 80 tokens is generous. |
| `Timeout` | `3 seconds` | The classifier must not delay the user; a timeout falls back to the heuristic. |

### Response Parsing

The classifier's text response is parsed by `extractJSONObject`, which finds the outermost `{` and `}` and extracts the JSON between them. This handles cases where the model wraps its response in markdown code blocks or adds explanatory text.

The parsed JSON must contain a `needs_plan` boolean field and an optional `reason` string. Missing `needs_plan` is treated as a parse error, which falls back to the heuristic.

---

## TaskWarrantsPlanner: Two-Model Mode

`TaskWarrantsPlanner(input)` is a simpler variant used in two-model mode, where a dedicated planner model drafts plans before the executor model acts. It skips planning for:

1. **Empty input**: No work to plan.
2. **Slash commands**: `/model`, `/effort`, etc. — these are UI operations, not work requests.
3. **Plan mode markers**: Already in plan mode.
4. **Low-risk questions**: Informational queries that don't change files.

Unlike `shouldAutoPlan`, `TaskWarrantsPlanner` does not use the LLM classifier — it relies solely on the `isLowRiskQuestion` heuristic. This keeps the two-model path fast: the planner model is only invoked for requests that clearly describe work.

---

## Configuration

The auto-plan system is controlled by the `auto_plan` configuration setting:

| Value | Behavior |
|---|---|
| `"off"` | Auto-planning is disabled; the user must manually enter plan mode. |
| `"on"` | Auto-planning is enabled; the system uses the heuristic + classifier pipeline. |
| `"ask"` | Legacy synonym for `"on"`, maintained for backward compatibility. |

The setting is normalized by `normalizeAutoPlan(mode)`, which lowercases and trims the input before matching.

---

## CJK Awareness

The auto-plan system is designed to work equally well with Chinese and English inputs. Both the complex intent terms and the low-risk question prefixes include Chinese equivalents, ensuring that:

- A Chinese request like "实现用户认证功能" (implement user authentication) is correctly classified as needing a plan.
- A Chinese question like "解释一下这段代码" (explain this code) is correctly classified as low-risk.
- Mixed-language inputs (common in Chinese developer communities) are handled naturally since both English and Chinese terms are checked against the lowercased input.
