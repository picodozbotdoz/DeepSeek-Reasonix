const {
  Document, Packer, Paragraph, TextRun, Table, TableRow, TableCell,
  PageBreak, Header, Footer, PageNumber, NumberFormat,
  AlignmentType, HeadingLevel, WidthType, BorderStyle, ShadingType,
  PageOrientation, LevelFormat, TableOfContents,
} = require("docx");
const fs = require("fs");

// ── Palette: GO-1 Graphite Orange (proposal/plan)
const P = {
  primary: "1A2330", body: "000000", secondary: "607080",
  accent: "D4875A", surface: "F8F0EB",
  cover: { titleColor: "FFFFFF", subtitleColor: "B0B8C0", metaColor: "90989F", footerColor: "687078" },
  table: { headerBg: "D4875A", headerText: "FFFFFF", accentLine: "D4875A", innerLine: "DDD0C8", surface: "F8F0EB" },
  bg: "1A2330",
};

const c = (hex) => hex.replace("#", "");
const t = P.table;

// ── Helper functions
function heading(text, level = HeadingLevel.HEADING_1) {
  const spacingMap = {
    [HeadingLevel.HEADING_1]: { before: 480, after: 200 },
    [HeadingLevel.HEADING_2]: { before: 360, after: 160 },
    [HeadingLevel.HEADING_3]: { before: 240, after: 120 },
  };
  return new Paragraph({
    heading: level,
    spacing: spacingMap[level] || { before: 240, after: 120 },
    children: [new TextRun({ text, bold: true, color: c(P.primary), font: { ascii: "Times New Roman", eastAsia: "SimHei" } })],
  });
}

function body(text) {
  return new Paragraph({
    alignment: AlignmentType.JUSTIFIED,
    indent: { firstLine: 480 },
    spacing: { line: 312, after: 80 },
    children: [new TextRun({ text, size: 24, color: c(P.body), font: { ascii: "Times New Roman", eastAsia: "SimSun" } })],
  });
}

function bodyNoIndent(text) {
  return new Paragraph({
    alignment: AlignmentType.JUSTIFIED,
    spacing: { line: 312, after: 80 },
    children: [new TextRun({ text, size: 24, color: c(P.body), font: { ascii: "Times New Roman", eastAsia: "SimSun" } })],
  });
}

function bodyBold(text) {
  return new Paragraph({
    alignment: AlignmentType.JUSTIFIED,
    indent: { firstLine: 480 },
    spacing: { line: 312, after: 80 },
    children: [new TextRun({ text, size: 24, bold: true, color: c(P.body), font: { ascii: "Times New Roman", eastAsia: "SimSun" } })],
  });
}

function bulletItem(text, level = 0) {
  return new Paragraph({
    bullet: { level },
    spacing: { line: 312, after: 40 },
    children: [new TextRun({ text, size: 24, color: c(P.body), font: { ascii: "Times New Roman", eastAsia: "SimSun" } })],
  });
}

function emptyLine() {
  return new Paragraph({ spacing: { after: 120 }, children: [] });
}

// Horizontal-Only table builder
function makeTable(headers, rows) {
  const headerCells = headers.map(h =>
    new TableCell({
      children: [new Paragraph({ children: [new TextRun({ text: h, bold: true, size: 21, color: c(t.headerText), font: { ascii: "Times New Roman", eastAsia: "SimHei" } })] })],
      shading: { type: ShadingType.CLEAR, fill: c(t.headerBg) },
      margins: { top: 60, bottom: 60, left: 120, right: 120 },
    })
  );
  const dataRows = rows.map((row, ri) =>
    new TableRow({
      tableHeader: false,
      cantSplit: true,
      children: row.map(cell =>
        new TableCell({
          children: [new Paragraph({ children: [new TextRun({ text: cell, size: 21, color: c(P.body), font: { ascii: "Times New Roman", eastAsia: "SimSun" } })] })],
          shading: ri % 2 === 0 ? { type: ShadingType.CLEAR, fill: c(t.surface) } : { type: ShadingType.CLEAR, fill: "FFFFFF" },
          margins: { top: 60, bottom: 60, left: 120, right: 120 },
        })
      ),
    })
  );
  return new Table({
    width: { size: 100, type: WidthType.PERCENTAGE },
    borders: {
      top: { style: BorderStyle.SINGLE, size: 2, color: c(t.accentLine) },
      bottom: { style: BorderStyle.SINGLE, size: 2, color: c(t.accentLine) },
      left: { style: BorderStyle.NONE },
      right: { style: BorderStyle.NONE },
      insideHorizontal: { style: BorderStyle.SINGLE, size: 1, color: c(t.innerLine) },
      insideVertical: { style: BorderStyle.NONE },
    },
    rows: [
      new TableRow({
        tableHeader: true,
        cantSplit: true,
        children: headerCells,
      }),
      ...dataRows,
    ],
  });
}

function tableCaption(text) {
  return new Paragraph({
    alignment: AlignmentType.CENTER,
    spacing: { before: 80, after: 200 },
    children: [new TextRun({ text, size: 21, italics: true, color: c(P.secondary), font: { ascii: "Times New Roman", eastAsia: "SimSun" } })],
  });
}

// ── Cover (R4 Top Color Block)
const NB = { style: BorderStyle.NONE, size: 0, color: "FFFFFF" };
const allNoBorders = { top: NB, bottom: NB, left: NB, right: NB, insideHorizontal: NB, insideVertical: NB };

function buildCover() {
  const title = "Reasonix Fleet Manager";
  const subtitle = "Architecture Proposal: Hub-and-Spoke with Operator Mode";
  const metaLines = [
    "Version: 1.0",
    "Date: June 7, 2026",
    "Status: Architecture Design Stage",
    "Classification: Internal",
  ];

  return [
    new Table({
      borders: allNoBorders,
      rows: [new TableRow({
        height: { value: 16838, rule: "exact" },
        children: [new TableCell({
          width: { size: 100, type: WidthType.PERCENTAGE },
          verticalAlign: "top",
          borders: allNoBorders,
          shading: { type: ShadingType.CLEAR, fill: c(P.bg) },
          children: [
            // Color block top area
            new Paragraph({ spacing: { before: 3600 }, children: [] }),
            new Paragraph({
              indent: { left: 1400, right: 1400 },
              children: [new TextRun({ text: title, size: 72, bold: true, color: c(P.cover.titleColor), font: { ascii: "Times New Roman", eastAsia: "SimHei" } })],
            }),
            new Paragraph({ spacing: { before: 300 }, indent: { left: 1400, right: 1400 },
              border: { top: { style: BorderStyle.SINGLE, size: 12, color: c(P.accent), space: 16 } },
              children: [],
            }),
            new Paragraph({
              indent: { left: 1400, right: 1400 },
              spacing: { before: 300 },
              children: [new TextRun({ text: subtitle, size: 32, color: c(P.cover.subtitleColor), font: { ascii: "Times New Roman", eastAsia: "SimHei" } })],
            }),
            new Paragraph({ spacing: { before: 2400 }, children: [] }),
            ...metaLines.map(line =>
              new Paragraph({
                indent: { left: 1400 },
                spacing: { after: 80 },
                children: [new TextRun({ text: line, size: 22, color: c(P.cover.metaColor), font: { ascii: "Times New Roman", eastAsia: "SimSun" } })],
              })
            ),
          ],
        })],
      })],
    }),
  ];
}

// ── Body content
function buildBody() {
  const children = [];

  // === Section 1: Executive Summary ===
  children.push(heading("1. Executive Summary"));
  children.push(body("This document presents the architecture proposal for the Reasonix Fleet Manager (RFM), a platform designed to manage, orchestrate, and monitor multiple Reasonix AI coding agent instances through the Model Context Protocol (MCP). The platform adopts a Hub-and-Spoke architecture where a central Management Hub coordinates a fleet of Reasonix worker instances, each assigned a dedicated role such as coder, reviewer, tester, or operations specialist."));
  children.push(body("A distinguishing feature of this proposal is the Operator Mode, which allows human users to intercept and directly drive any worker instance in real time. This capability bridges the gap between fully automated agent orchestration and human-in-the-loop control, enabling users to correct agent behavior mid-task, provide domain-specific guidance, or take over when automated approaches reach their limits."));
  children.push(body("The platform provides a web-based dashboard for real-time fleet visibility, a role-based task scheduler for intelligent work distribution, an Operator Gateway for secure human-to-agent connections, and comprehensive telemetry for operational insight. The design prioritizes simplicity, security, and incremental extensibility, making it suitable for teams of 2 to 20 Reasonix instances in its initial release while preserving a clear upgrade path to distributed and Kubernetes-native architectures."));

  // === Section 2: Problem Statement ===
  children.push(heading("2. Problem Statement"));
  children.push(heading("2.1 The Multi-Agent Coordination Challenge", HeadingLevel.HEADING_2));
  children.push(body("As organizations adopt AI coding agents like Reasonix for software development workflows, they increasingly run multiple agent instances simultaneously. A typical team might have one agent writing code, another reviewing it, a third running tests, and a fourth handling documentation. Without a coordination layer, these agents operate in isolation, leading to duplicated effort, conflicting changes, uncoordinated task execution, and no centralized visibility into the fleet's state and performance."));
  children.push(heading("2.2 The Human Override Gap", HeadingLevel.HEADING_2));
  children.push(body("Existing multi-agent orchestration tools treat agents as fully autonomous entities. Once a task is dispatched, the system has no mechanism for a human to step in and redirect, correct, or supplement the agent's work in real time. This is a critical limitation because AI agents, however capable, inevitably encounter situations where human judgment is needed: ambiguous requirements, novel architectural decisions, security-sensitive operations, or simply cases where the agent is going down the wrong path. The ability to intercept, drive, and release an agent mid-task is not a luxury but a necessity for production-grade agent fleets."));
  children.push(heading("2.3 Market Gap Analysis", HeadingLevel.HEADING_2));
  children.push(body("Research into existing projects reveals that no single solution combines MCP-based coordination, role assignment, a monitoring dashboard, and human operator override. Projects like Maestro and Composio Agent Orchestrator provide fleet management without MCP backbone or role systems. Ruflo/Claude Flow offers MCP-native swarm coordination but lacks a web dashboard and is terminal-only. MCP Agent Mail provides agent identity concepts but no management UI. The Claude Code Agent Monitor delivers a real-time dashboard but is monitoring-only with no role assignment or orchestration. The Reasonix Fleet Manager fills this gap by unifying all four capabilities in a single platform purpose-built for managing Reasonix instances."));

  // === Section 3: Goals and Non-Goals ===
  children.push(heading("3. Goals and Non-Goals"));
  children.push(heading("3.1 Goals", HeadingLevel.HEADING_2));
  children.push(bulletItem("Provide centralized management of multiple Reasonix instances via MCP"));
  children.push(bulletItem("Implement role-based task assignment and role-specific permission policies"));
  children.push(bulletItem("Deliver a real-time web dashboard showing fleet status, worker details, task progress, and statistics"));
  children.push(bulletItem("Enable Operator Mode: human users can intercept, drive, and release any worker instance"));
  children.push(bulletItem("Support seamless transitions between automated and operator modes without data loss"));
  children.push(bulletItem("Maintain full audit trail of mode transitions, user actions, and task state changes"));
  children.push(bulletItem("Support 2-20 worker instances in the initial release"));
  children.push(bulletItem("Provide both web-based and CLI interfaces for fleet management and operator connections"));
  children.push(heading("3.2 Non-Goals", HeadingLevel.HEADING_2));
  children.push(bulletItem("Multi-tenant or SaaS deployment (single-organization in v1)"));
  children.push(bulletItem("Supporting agent types other than Reasonix (extensible in future versions)"));
  children.push(bulletItem("Peer-to-peer inter-agent communication (agents communicate only through the Hub)"));
  children.push(bulletItem("Auto-scaling or Kubernetes-native orchestration (future evolution, not v1)"));
  children.push(bulletItem("Replacing existing MCP gateways (complements them, does not replace)"));

  // === Section 4: Architecture Overview ===
  children.push(heading("4. Architecture Overview"));
  children.push(heading("4.1 Hub-and-Spoke Topology", HeadingLevel.HEADING_2));
  children.push(body("The Reasonix Fleet Manager uses a Hub-and-Spoke architecture where the Management Hub serves as the central coordination point and each Reasonix instance operates as an independent spoke. The Hub communicates with workers via MCP (Model Context Protocol) and ACP (Agent Client Protocol), while users interact with the Hub through a web dashboard, CLI tool, or IDE extension. The Hub never directly modifies worker code; instead, it issues commands through the standardized ACP protocol that each Reasonix instance already supports."));
  children.push(body("This topology provides a single point of control for the fleet, simplifies role and policy enforcement, enables comprehensive monitoring through centralized telemetry collection, and ensures that all inter-agent coordination flows through a well-defined channel. The trade-off is that the Hub becomes a single point of failure, which is mitigated through persistent state storage and fast restart capabilities."));

  children.push(heading("4.2 Component Diagram", HeadingLevel.HEADING_2));
  children.push(body("The system comprises seven core components organized in a layered architecture. At the top layer, the Dashboard (React SPA) and CLI Tool provide user-facing interfaces. The middle layer contains the Hub's core logic: the Session Router for ownership tracking, the Role Manager for role-based policies, the Task Scheduler for work distribution, and the Operator Gateway for human-to-agent connections. The bottom layer consists of the Hub MCP Server (the communication backbone) and the Worker Adapters that wrap each Reasonix ACP server as an MCP endpoint."));

  children.push(makeTable(
    ["Component", "Responsibility"],
    [
      ["Hub MCP Server", "Routes MCP tool calls between workers and the Hub; aggregates metrics and telemetry; manages worker connections and health checks"],
      ["Session Router", "Tracks session ownership (Hub vs. User); implements locking semantics; manages mode transitions (automated, operator, hybrid)"],
      ["Task Scheduler", "Queues and dispatches tasks to workers by role, availability, and load; supports priority-based and dependency-aware scheduling"],
      ["Role Manager", "Defines and enforces role-based permissions per worker; controls tool access, MCP server access, and permission levels"],
      ["Operator Gateway", "ACP-to-WebSocket bridge for user connections; multiplexes operator sessions; passes through permission requests to human operators"],
      ["Dashboard", "React SPA for fleet overview, worker detail views, task tracking, operator console, and configuration management"],
      ["Worker Adapter", "Thin wrapper around each reasonix acp process that exposes it as an MCP server endpoint to the Hub"],
    ]
  ));
  children.push(tableCaption("Table 1: Core Components and Responsibilities"));

  // === Section 5: Core Architecture ===
  children.push(heading("5. Core Architecture Design"));
  children.push(heading("5.1 Hub MCP Server", HeadingLevel.HEADING_2));
  children.push(body("The Hub MCP Server is the communication backbone of the fleet. It acts as both an MCP client (connecting to each worker's MCP endpoint) and an MCP server (exposing fleet-level tools to the dashboard and external consumers). Each worker is registered as a remote MCP server, and the Hub discovers their tools, routes calls, and collects execution telemetry. The server maintains a connection pool with configurable concurrency limits (default: 8 parallel handshakes) and implements heartbeat monitoring to detect offline workers within a configurable interval (default: 30 seconds)."));
  children.push(body("When a worker goes offline, the Hub marks it as unavailable, reassigns its queued tasks to other workers with compatible roles, and logs the event for operational review. When a worker comes back online, the Hub automatically reconnects, resynchronizes its state, and resumes task assignment. The Hub MCP Server also implements request multiplexing: multiple dashboard users can observe the same worker's state simultaneously, but only one entity (the Hub scheduler or one human operator) can send prompts at a time, enforced by the Session Router's locking mechanism."));

  children.push(heading("5.2 Session Router", HeadingLevel.HEADING_2));
  children.push(body("The Session Router is the authority on who controls each worker at any given time. It maintains a state machine for each worker with three primary modes: Automated (Hub drives), Operator (human user drives), and Idle (no active session). Mode transitions are atomic and logged to the Event Store for audit purposes. When a user requests operator access, the Session Router checks availability, initiates the mode transition, pauses any in-flight automated task, and grants the user exclusive control. When the user releases the worker, the Router determines the continuation strategy and transitions back to automated or idle mode."));
  children.push(body("The Session Router also enforces concurrency guarantees: only one operator can control a worker at a time, the Hub scheduler cannot send prompts to a worker in operator mode, and operator locks have configurable timeout values (default: 60 minutes of inactivity) to prevent workers from being locked indefinitely by abandoned sessions. If a user's connection drops unexpectedly, the Router detects the disconnection, waits for a grace period (default: 5 minutes), and then automatically releases the worker back to automated mode."));

  children.push(makeTable(
    ["Worker Mode", "Owner", "Hub Scheduler", "User Connection", "Transition Trigger"],
    [
      ["Automated", "Hub", "Active (sends prompts)", "Read-only (observe)", "Default; or user releases"],
      ["Operator", "User", "Paused (queued)", "Active (sends prompts)", "User requests takeover"],
      ["Idle", "None", "Waiting for tasks", "Read-only (observe)", "No tasks assigned"],
    ]
  ));
  children.push(tableCaption("Table 2: Worker Mode State Machine"));

  children.push(heading("5.3 Role Manager", HeadingLevel.HEADING_2));
  children.push(body("The Role Manager defines and enforces role-based policies for each worker. A role specifies which tools the worker can access, which MCP servers it can connect to, what permission level it operates under, and how many concurrent tasks it can handle. Roles are assigned at worker registration time and can be changed dynamically through the dashboard or CLI. The Role Manager validates task assignments against role capabilities, ensuring that a worker assigned the 'reviewer' role cannot execute write operations, and a 'tester' role cannot modify source code directly."));

  children.push(makeTable(
    ["Role", "Allowed Tools", "Permission Level", "Max Concurrent Tasks"],
    [
      ["Coder", "read_file, edit_file, write_file, bash, glob, grep", "auto-edit", "3"],
      ["Reviewer", "read_file, glob, grep, bash (read-only)", "ask", "5"],
      ["Tester", "read_file, bash, glob, grep", "auto-edit", "2"],
      ["Ops", "bash, read_file, write_file (config only)", "plan", "1"],
      ["Architect", "read_file, glob, grep (all read-only)", "ask", "5"],
      ["Docs", "read_file, write_file, glob", "auto-edit", "3"],
    ]
  ));
  children.push(tableCaption("Table 3: Default Role Definitions"));

  children.push(body("Beyond the default roles, the Role Manager supports custom role definitions created through the dashboard or configuration files. Each custom role specifies allowed tool name patterns (supporting wildcards like mcp__* for all MCP tools), allowed MCP server names, the permission policy (full-auto, auto-edit, plan, or ask), and the maximum number of concurrent tasks. This extensibility ensures that organizations can model their specific team structures and workflow requirements within the fleet management system."));

  children.push(heading("5.4 Task Scheduler", HeadingLevel.HEADING_2));
  children.push(body("The Task Scheduler manages the lifecycle of work items from submission to completion. It maintains a priority queue where tasks are ordered by priority level (critical, high, normal, low), creation time, and role requirements. When a worker becomes available, the scheduler selects the highest-priority task whose role requirements match the worker's assigned role and dispatches it via the Hub MCP Server. The scheduler supports task dependencies: a task can specify prerequisite tasks that must complete before it becomes eligible for assignment, enabling multi-step workflows like 'code, then review, then test'."));
  children.push(body("The scheduler implements several dispatch strategies. The default is 'role-match first-come first-served', which assigns tasks to the first available worker with a matching role. Alternative strategies include 'load-balanced', which distributes tasks to the least-busy compatible worker, and 'affinity-based', which preferentially assigns tasks to workers that have previously handled similar tasks (to benefit from cached context and MCP connections). The scheduler also handles task reassignment when a worker goes offline mid-task: the task is re-queued with its progress state preserved, and the next available compatible worker can resume from a checkpoint."));

  children.push(heading("5.5 Worker Adapter", HeadingLevel.HEADING_2));
  children.push(body("Each Reasonix instance runs in ACP server mode (reasonix acp), exposing a stdio JSON-RPC 2.0 interface. The Worker Adapter is a thin process that wraps this ACP interface and exposes it as an MCP server endpoint that the Hub can connect to. The adapter handles transport translation (ACP stdio to MCP stdio/HTTP), lifecycle management (starting and stopping the reasonix acp process), health monitoring (heartbeat checks and crash detection), and session multiplexing (allowing the Hub to observe while an operator drives)."));
  children.push(body("The adapter is configured with the worker's name, role, working directory, model, and MCP server list. On startup, it launches the reasonix acp subprocess, performs the ACP initialize handshake, and registers the worker's tools with the Hub. The adapter translates between ACP notifications (agent_message_chunk, tool_call, etc.) and MCP tool results that the Hub can process and forward to the dashboard. When the adapter detects that the reasonix acp process has crashed, it automatically restarts it and re-registers with the Hub, preserving the worker's identity and role assignment."));

  // === Section 6: Operator Mode ===
  children.push(heading("6. Operator Mode: Human-in-the-Loop Control"));
  children.push(heading("6.1 Design Philosophy", HeadingLevel.HEADING_2));
  children.push(body("Operator Mode is the defining feature of the Reasonix Fleet Manager. It recognizes that fully autonomous agent orchestration, while efficient for routine tasks, is insufficient for production environments where human judgment, domain expertise, and safety oversight are non-negotiable. The design philosophy is that automation and human control are not opposites but complementary modes that should transition seamlessly within the same system, without data loss, context reset, or operational disruption."));
  children.push(body("The key principle is session ownership transfer: when a user takes over a worker, the Hub gracefully pauses its automated task, transfers session ownership to the user, and bridges the user's connection directly to the worker's ACP session. The user then drives the agent exactly as if they were using Reasonix directly in their terminal, with full access to all tools, real-time streaming updates, and interactive permission approvals. When the user releases the worker, the Hub determines the continuation strategy and resumes automation from an appropriate state."));

  children.push(heading("6.2 Three Interaction Modes", HeadingLevel.HEADING_2));
  children.push(body("Each worker operates in one of three interaction modes at any given time. In Automated Mode, the Hub Task Scheduler drives the worker by sending session/prompt requests through MCP. The user observes results on the dashboard but does not intervene. This is the default and most common mode for routine task execution. In Operator Mode, a human user connects to the worker through the Operator Gateway and drives it directly by sending prompts, approving permissions, and providing guidance. The Hub's scheduler is paused for that worker, and all session/update events flow to both the Hub (for logging) and the user (for driving). In Hybrid Mode, a user intercepts a worker mid-task in automated mode, makes corrections or provides redirection, and then releases it back to automated mode. The Hub's task queue is paused during the intervention and resumed from an appropriate checkpoint when the user releases."));

  children.push(heading("6.3 Operator Gateway", HeadingLevel.HEADING_2));
  children.push(body("The Operator Gateway is an ACP-to-WebSocket bridge that enables human users to connect to workers through the Hub without direct access to the worker's process. This design choice provides several advantages over direct ACP connections: the Hub retains full visibility into operator sessions (enabling live dashboard updates and audit logging), the Hub can enforce safety policies (such as preventing operators from accessing workers outside their authorized roles), the Hub handles connection management (including graceful disconnection handling and automatic session release), and the Hub prevents conflicting operator takeovers through its Session Router locking mechanism."));
  children.push(body("The Gateway translates WebSocket messages from the user's client (dashboard, CLI, or IDE extension) into ACP session/prompt requests to the worker. Conversely, it translates ACP session/update notifications from the worker into WebSocket events for the client. Permission requests from the worker (session/request_permission in ACP) are forwarded to the operator's client as interactive dialogs, and the operator's response is translated back into an ACP permission response. This bidirectional translation ensures that the operator has the same interactive experience as a direct Reasonix CLI user, but with the Hub's safety and observability layer in between."));

  children.push(heading("6.4 User Connection Methods", HeadingLevel.HEADING_2));
  children.push(heading("6.4.1 Web Dashboard Operator Console", HeadingLevel.HEADING_3));
  children.push(body("The simplest way for a user to take over a worker is through the dashboard's built-in Operator Console. The fleet view shows all workers with their current mode, owner, and status. Clicking 'Take Over' on a worker initiates the operator mode transition, and a chat panel opens connected to that worker's ACP session through the Operator Gateway. The user can type messages, see the agent's responses stream in real time, approve or reject permission requests, and click 'Release Worker' when done. This method requires no additional software and is the recommended path for most users."));

  children.push(heading("6.4.2 CLI Tool (reasonixctl)", HeadingLevel.HEADING_3));
  children.push(body("Power users can use the reasonixctl CLI tool for operator connections. The CLI provides commands for listing workers (reasonixctl workers list), connecting to a specific worker (reasonixctl connect worker-1), releasing a worker (reasonixctl release worker-1), and peeking at a worker's current state without taking over (reasonixctl peek worker-1). The CLI connection tunnels through the Hub's Operator Gateway by default but also supports a --direct flag for bypassing the Hub entirely (for debugging or emergency access). When connected, the CLI renders the agent's streaming output, tool calls, and permission requests in the terminal, providing a native Reasonix-like experience."));

  children.push(heading("6.4.3 IDE Extension", HeadingLevel.HEADING_3));
  children.push(body("An IDE extension (initially targeting VS Code and Cursor) allows developers to select a worker from a sidebar panel and drive it from within their editor. The chat interface in the IDE connects to the Hub's Operator Gateway, and the agent's file operations (read, edit, write) are reflected in the editor's file tree in real time. This tight integration provides the most natural operator experience for developers who are already working in their IDE alongside the agents they are managing."));

  children.push(heading("6.5 Takeover and Release Flow", HeadingLevel.HEADING_2));
  children.push(body("The takeover flow begins when a user requests operator access to a worker. The Hub's Session Router checks whether the worker is available (not already in operator mode by another user). If the worker is in automated mode and mid-task, the Hub asks the user to confirm the interruption. Upon confirmation, the Hub sends session/cancel to the worker (if mid-turn), records the intercept event in the Event Store (including the paused task ID and user identity), locks the worker to the requesting user, creates or reuses an ACP session through the Operator Gateway, and bridges the user's connection to the worker. The dashboard updates to show 'Worker X - Operator: username'."));
  children.push(body("The release flow begins when the user ends their operator session (by clicking 'Release Worker' in the dashboard, pressing Ctrl+C in the CLI, or closing the IDE panel). The Hub records the operator session transcript, unlocks the worker, and determines the continuation strategy based on the user's choice or system default. The available strategies are: Resume (continue the interrupted task from where it was paused), Fresh (discard operator session context and start the next queued task), Continue from here (treat the operator's last state as the new starting point for the same task), and Ask user (present a dialog letting the operator choose). The default strategy is Ask user, ensuring the operator's intent is always respected."));

  children.push(heading("6.6 Direct Connection Mode", HeadingLevel.HEADING_2));
  children.push(body("While the Hub-tunneled Operator Gateway is the recommended path, a direct connection mode is also supported for power users and debugging scenarios. In direct mode, the user connects to the worker's ACP endpoint directly (via a local TCP port or Unix socket exposed by the Worker Adapter), bypassing the Hub entirely. This provides the lowest-latency connection and full access to all worker capabilities, but comes with trade-offs: the dashboard loses real-time visibility into the operator session, the Hub cannot enforce safety policies or prevent conflicting takeovers, and no automatic session release occurs if the user disconnects. Direct mode should be used only for debugging or emergency access, and the Hub should be notified (via reasonixctl notify worker-1 --direct) so it can mark the worker as 'externally controlled' in the dashboard."));

  // === Section 7: Data Model ===
  children.push(heading("7. Data Model"));

  children.push(heading("7.1 Worker", HeadingLevel.HEADING_2));
  children.push(makeTable(
    ["Field", "Type", "Description"],
    [
      ["id", "UUID", "Unique worker identifier"],
      ["name", "string", "Human-readable worker name (e.g., 'coder-1', 'reviewer-primary')"],
      ["role", "string", "Assigned role name (references Role definition)"],
      ["mode", "enum", "Current interaction mode: automated | operator | idle"],
      ["owner", "string", "Current session owner: 'hub' | user_id | null"],
      ["status", "enum", "Current status: running | waiting | error | offline"],
      ["acp_endpoint", "string", "Worker ACP connection endpoint (host:port or unix socket)"],
      ["config", "JSON", "Worker configuration: model, cwd, mcpServers, permission level"],
      ["stats", "JSON", "Runtime statistics: tasks_completed, tokens_used, uptime, error_count"],
      ["last_heartbeat", "timestamp", "Last heartbeat received from the worker adapter"],
      ["created_at", "timestamp", "Worker registration time"],
      ["updated_at", "timestamp", "Last state change time"],
    ]
  ));
  children.push(tableCaption("Table 4: Worker Entity Schema"));

  children.push(heading("7.2 Task", HeadingLevel.HEADING_2));
  children.push(makeTable(
    ["Field", "Type", "Description"],
    [
      ["id", "UUID", "Unique task identifier"],
      ["description", "string", "Task description and instructions for the agent"],
      ["role_requirement", "string", "Required worker role to execute this task"],
      ["status", "enum", "queued | assigned | in_progress | paused | completed | failed"],
      ["assigned_worker_id", "UUID (nullable)", "Currently assigned worker, null if unassigned"],
      ["parent_task_id", "UUID (nullable)", "Parent task for sub-task hierarchies"],
      ["priority", "enum", "critical | high | normal | low"],
      ["dependencies", "UUID[]", "Task IDs that must complete before this task is eligible"],
      ["created_at", "timestamp", "Task creation time"],
      ["started_at", "timestamp", "When execution began"],
      ["completed_at", "timestamp", "When execution completed or failed"],
      ["result_summary", "text", "Agent's final output or error description"],
    ]
  ));
  children.push(tableCaption("Table 5: Task Entity Schema"));

  children.push(heading("7.3 OperatorSession", HeadingLevel.HEADING_2));
  children.push(makeTable(
    ["Field", "Type", "Description"],
    [
      ["id", "UUID", "Unique operator session identifier"],
      ["worker_id", "UUID", "Worker that was operated on"],
      ["user_id", "string", "User who took operator control"],
      ["started_at", "timestamp", "When operator mode was activated"],
      ["ended_at", "timestamp", "When operator mode was released"],
      ["mode_on_start", "enum", "Worker's mode before takeover: automated | idle"],
      ["paused_task_id", "UUID (nullable)", "Task that was paused during takeover"],
      ["release_strategy", "enum", "resume | fresh | continue | ask"],
      ["transcript_path", "string", "Path to the operator session transcript file"],
    ]
  ));
  children.push(tableCaption("Table 6: OperatorSession Entity Schema"));

  children.push(heading("7.4 Role", HeadingLevel.HEADING_2));
  children.push(makeTable(
    ["Field", "Type", "Description"],
    [
      ["name", "string", "Unique role identifier (coder, reviewer, tester, ops, architect, docs)"],
      ["allowed_tools", "string[]", "Glob patterns for allowed tool names (e.g., 'read_file', 'mcp__*')"],
      ["allowed_mcp_servers", "string[]", "Names of MCP servers this role can access"],
      ["permission_level", "enum", "full_auto | auto_edit | plan | ask"],
      ["max_concurrent_tasks", "int", "Maximum number of tasks this role can handle simultaneously"],
      ["description", "string", "Human-readable description of the role's purpose and scope"],
    ]
  ));
  children.push(tableCaption("Table 7: Role Entity Schema"));

  children.push(heading("7.5 EventStore", HeadingLevel.HEADING_2));
  children.push(body("The Event Store is an append-only log that records all significant state transitions in the system. Events include worker registration, mode transitions, task assignments, operator takeovers and releases, permission approvals and rejections, worker health changes, and configuration updates. Each event includes a timestamp, the affected entity IDs, the user or system component that triggered the event, and a JSON payload with event-specific details. The Event Store serves three purposes: audit trail for compliance and debugging, data source for dashboard real-time updates via WebSocket subscriptions, and historical analytics for operational optimization (e.g., identifying workers that are frequently taken over, tasks that consistently fail, or roles that are underutilized)."));

  // === Section 8: API Design ===
  children.push(heading("8. API Design"));
  children.push(heading("8.1 Hub REST API", HeadingLevel.HEADING_2));
  children.push(body("The Hub exposes a RESTful API for fleet management operations. Authentication is via API key or OAuth2 token. All endpoints return JSON and follow standard HTTP status codes. The API is versioned (v1) and designed to be stable across minor releases."));

  children.push(makeTable(
    ["Method", "Endpoint", "Description"],
    [
      ["GET", "/api/v1/workers", "List all workers with status, role, and mode"],
      ["GET", "/api/v1/workers/:id", "Get detailed worker info including config and stats"],
      ["POST", "/api/v1/workers", "Register a new worker"],
      ["PUT", "/api/v1/workers/:id", "Update worker config or role assignment"],
      ["DELETE", "/api/v1/workers/:id", "Deregister and stop a worker"],
      ["POST", "/api/v1/workers/:id/takeover", "Request operator mode on a worker"],
      ["POST", "/api/v1/workers/:id/release", "Release worker back to automated mode"],
      ["GET", "/api/v1/tasks", "List all tasks with status and assignment"],
      ["POST", "/api/v1/tasks", "Submit a new task to the queue"],
      ["GET", "/api/v1/tasks/:id", "Get task details and result"],
      ["DELETE", "/api/v1/tasks/:id", "Cancel a queued or in-progress task"],
      ["GET", "/api/v1/roles", "List all role definitions"],
      ["POST", "/api/v1/roles", "Create a custom role definition"],
      ["PUT", "/api/v1/roles/:name", "Update a role definition"],
      ["GET", "/api/v1/events", "Query event store with filters"],
    ]
  ));
  children.push(tableCaption("Table 8: Hub REST API Endpoints"));

  children.push(heading("8.2 WebSocket API", HeadingLevel.HEADING_2));
  children.push(body("The Hub exposes a WebSocket endpoint (/ws/v1/stream) for real-time updates. Clients subscribe to event channels (fleet, worker:{id}, task:{id}) and receive push notifications for state changes. The WebSocket API also carries operator mode traffic: when a user is in operator mode, their messages to the worker and the worker's streaming responses are carried over the WebSocket connection, bridged to the worker's ACP session by the Operator Gateway. The WebSocket protocol uses JSON messages with a type field for demultiplexing (e.g., type: 'session/update', type: 'session/prompt', type: 'session/request_permission')."));

  children.push(heading("8.3 MCP Server API", HeadingLevel.HEADING_2));
  children.push(body("The Hub itself exposes an MCP server interface that external tools can connect to. This allows MCP-compatible clients (IDEs, other agents, or the Reasonix instances themselves) to query fleet status, submit tasks, and retrieve worker information through the standard MCP protocol. The Hub's MCP server exposes tools like fleet_status (returns summary of all workers), worker_detail (returns detailed info for a specific worker), task_submit (submits a new task), and task_status (returns task progress and result). This MCP interface makes the Fleet Manager composable: other systems can integrate with it using the same MCP protocol that Reasonix already speaks."));

  // === Section 9: Security Model ===
  children.push(heading("9. Security Model"));
  children.push(heading("9.1 Authentication and Authorization", HeadingLevel.HEADING_2));
  children.push(body("All API and WebSocket connections to the Hub require authentication. The Hub supports two authentication methods: API key (for service-to-service communication and CLI tools) and OAuth2 token (for dashboard and IDE extension users). Authorization is role-based at two levels: user roles (admin, operator, viewer) control what management actions a user can perform, and worker roles (coder, reviewer, etc.) control what the agent itself can do. An admin can manage the fleet, assign roles, and take over any worker. An operator can take over workers within their authorized scope. A viewer can only observe the dashboard without operator capabilities."));

  children.push(heading("9.2 Operator Mode Security", HeadingLevel.HEADING_2));
  children.push(body("Operator Mode introduces specific security considerations. The Hub enforces that only one user can operate a worker at a time (preventing conflicting actions). Operator sessions are fully logged to the Event Store, including all prompts sent, tool calls made, and permission decisions taken. The Hub can restrict operator access based on user roles and worker roles (e.g., only senior developers can operate the 'ops' worker). Operator locks have configurable timeout values to prevent indefinite lockout. All operator traffic flows through the Hub's Operator Gateway by default, ensuring policy enforcement and audit logging even during human-controlled sessions."));

  children.push(heading("9.3 Worker Isolation", HeadingLevel.HEADING_2));
  children.push(body("Each worker runs in its own process with its own working directory, MCP server connections, and permission policy. Workers cannot directly communicate with each other; all coordination goes through the Hub. This isolation prevents a compromised or misbehaving worker from affecting other workers or the Hub itself. The Worker Adapter enforces resource limits (CPU, memory, file descriptors) and terminates workers that exceed their budget. File system access is sandboxed to the worker's assigned working directory, and the Role Manager's tool restrictions prevent workers from accessing tools outside their role's scope."));

  // === Section 10: Dashboard Design ===
  children.push(heading("10. Dashboard Design"));
  children.push(heading("10.1 Fleet Overview", HeadingLevel.HEADING_2));
  children.push(body("The fleet overview is the landing page of the dashboard. It displays a grid of worker cards, each showing the worker's name, role, current mode (automated/operator/idle), status (running/waiting/error/offline), and a brief summary of the current or last task. Color coding provides instant visual status: green for running, blue for operator mode, yellow for waiting, and red for error. The overview also shows aggregate fleet statistics: total tasks completed, tasks in progress, average task duration, total tokens consumed, and fleet utilization percentage. A global task queue panel shows the next tasks waiting for assignment."));

  children.push(heading("10.2 Worker Detail View", HeadingLevel.HEADING_2));
  children.push(body("Clicking on a worker card opens the worker detail view, which provides comprehensive information about a specific worker. The view includes the worker's current session transcript (scrolling log of prompts, responses, and tool calls), the worker's configuration (model, working directory, MCP servers, permission level), runtime statistics (tasks completed, tokens consumed, uptime, average task duration), the worker's role definition and permission scope, and the worker's event history (recent mode transitions, errors, and configuration changes). If the worker is in operator mode, the detail view also shows who is operating it and how long they have been in control."));

  children.push(heading("10.3 Operator Console", HeadingLevel.HEADING_2));
  children.push(body("The operator console is a chat-style interface embedded in the worker detail view. When a user clicks 'Take Over', the console activates and connects to the worker through the Operator Gateway. The console displays the agent's streaming output (reasoning chunks and message chunks), tool calls with their parameters and results (expandable for detail), and permission request dialogs with approve/reject buttons. The user types messages in an input field at the bottom and presses Enter or clicks Send to submit. A 'Release Worker' button is always visible, along with a dropdown for choosing the release strategy (resume, fresh, continue, ask). The console also supports pasting images and file references if the Reasonix version supports those content types."));

  children.push(heading("10.4 Task and Configuration Views", HeadingLevel.HEADING_2));
  children.push(body("The task management view shows the global task queue with filtering by status, priority, role requirement, and assigned worker. Users can submit new tasks, cancel queued tasks, and view completed task results. The configuration view allows administrators to manage worker registrations, role definitions, and fleet-wide settings (default permission levels, heartbeat intervals, operator timeout values, and logging verbosity). All configuration changes are versioned and auditable through the Event Store."));

  // === Section 11: Technology Stack ===
  children.push(heading("11. Technology Stack"));

  children.push(makeTable(
    ["Layer", "Technology", "Rationale"],
    [
      ["Hub Backend", "Go", "High concurrency, same language as Reasonix, excellent MCP/ACP library support"],
      ["Dashboard Frontend", "React + TypeScript + Tailwind CSS", "Component-based UI, real-time WebSocket support, rapid prototyping"],
      ["Database", "SQLite (v1) / PostgreSQL (v2)", "Zero-config for small deployments; upgrade path for production"],
      ["Event Store", "SQLite append-only table", "Simple, reliable, queryable; upgrade to Kafka/NATS for v2"],
      ["Worker Communication", "MCP (JSON-RPC 2.0) + ACP (stdio)", "Native Reasonix protocols, no custom wire format needed"],
      ["Operator Gateway", "WebSocket + ACP bridge", "Standard web protocol for browser/IDE connectivity"],
      ["CLI Tool", "Go (reasonixctl)", "Consistent with Hub backend, single binary distribution"],
      ["Authentication", "API Key + JWT (v1)", "Simple to implement; OAuth2 integration in v2"],
      ["Monitoring", "Prometheus metrics + Grafana dashboards", "Industry standard, extensible, existing ecosystem"],
      ["Containerization", "Docker Compose (v1) / Helm (v2)", "Easy local development; K8s-native for production"],
    ]
  ));
  children.push(tableCaption("Table 9: Technology Stack"));

  // === Section 12: Deployment ===
  children.push(heading("12. Deployment Architecture"));
  children.push(heading("12.1 Local Development", HeadingLevel.HEADING_2));
  children.push(body("For local development and small teams, all components run in a single Docker Compose stack. The Hub, Dashboard, and SQLite database run as separate containers, while Reasonix workers run as native processes (to support the reasonix acp subprocess model). The Worker Adapters connect to the Hub container over the Docker network. This setup requires only Docker and Docker Compose, with no external dependencies."));

  children.push(heading("12.2 Production Deployment", HeadingLevel.HEADING_2));
  children.push(body("For production deployments, the Hub and Dashboard run behind a reverse proxy (Nginx or Traefik) with TLS termination. The database is upgraded to PostgreSQL for concurrent access and backup capabilities. Workers can run on separate machines, connecting to the Hub over the network via the Worker Adapter's HTTP transport mode. The Operator Gateway is exposed through the same reverse proxy with WebSocket support. Monitoring is handled by Prometheus scraping Hub metrics and Grafana dashboards for visualization. All components are containerized and orchestrated via Docker Compose initially, with a Helm chart available for Kubernetes deployments."));

  // === Section 13: Implementation Roadmap ===
  children.push(heading("13. Implementation Roadmap"));

  children.push(makeTable(
    ["Phase", "Timeline", "Deliverables"],
    [
      ["Phase 1: Foundation", "Weeks 1-3", "Hub MCP Server, Worker Adapter, Session Router, basic fleet management API"],
      ["Phase 2: Scheduling", "Weeks 4-5", "Task Scheduler with priority queue, Role Manager with default roles, task submission API"],
      ["Phase 3: Dashboard", "Weeks 6-8", "React dashboard with fleet overview, worker detail view, task management, real-time updates"],
      ["Phase 4: Operator Mode", "Weeks 9-11", "Operator Gateway, WebSocket bridge, operator console in dashboard, takeover/release flows"],
      ["Phase 5: CLI and Polish", "Weeks 12-13", "reasonixctl CLI tool, authentication, Event Store analytics, documentation"],
      ["Phase 6: Testing and Release", "Weeks 14-16", "Integration testing, load testing, security audit, v1.0 release"],
    ]
  ));
  children.push(tableCaption("Table 10: Implementation Roadmap"));

  // === Section 14: Risks and Mitigations ===
  children.push(heading("14. Risk Analysis and Mitigations"));

  children.push(makeTable(
    ["Risk", "Likelihood", "Impact", "Mitigation"],
    [
      ["Hub becomes single point of failure", "Medium", "High", "Persistent state in SQLite; fast restart (<10s); automatic worker reconnection"],
      ["Operator mode causes context loss on transition", "Medium", "Medium", "Full transcript logging; continuation strategies; checkpoint-based resume"],
      ["Worker process crashes during operator mode", "Low", "High", "Worker Adapter auto-restart; operator notified immediately; session state preserved in transcript"],
      ["Multiple users attempt simultaneous takeover", "Medium", "Low", "Session Router locking; clear UI feedback on worker availability; queuing for popular workers"],
      ["MCP protocol version incompatibility", "Low", "Medium", "Pin to MCP protocol version 2024-11-05; version negotiation in Worker Adapter handshake"],
      ["Performance degradation with many workers", "Low", "Medium", "Connection pooling; configurable concurrency limits; telemetry-driven scaling recommendations"],
    ]
  ));
  children.push(tableCaption("Table 11: Risk Analysis"));

  // === Section 15: Future Evolution ===
  children.push(heading("15. Future Evolution"));
  children.push(heading("15.1 Phase 2: Distributed Architecture", HeadingLevel.HEADING_2));
  children.push(body("The Hub-and-Spoke architecture is designed to evolve into a distributed system (Option B from the architecture evaluation). This involves adding a sidecar process to each worker that handles service registration, peer-to-peer messaging via an event bus (NATS or Redis Streams), and a service registry (etcd or Consul) for dynamic discovery. The Hub transitions from a central controller to a Control Plane, and workers gain the ability to communicate directly for collaborative tasks. This evolution preserves all v1 functionality while adding horizontal scalability, fault tolerance, and inter-agent collaboration."));

  children.push(heading("15.2 Phase 3: Kubernetes-Native PaaS", HeadingLevel.HEADING_2));
  children.push(body("For enterprise deployments and organizations already running Kubernetes, the Fleet Manager can be packaged as a set of Custom Resource Definitions (CRDs) and a Kubernetes Operator. Workers run as Pods with sidecar containers, roles are implemented as Pod labels, and the Platform API Server manages the fleet via the Kubernetes API. This provides auto-scaling, self-healing, resource limits, namespace isolation, and cloud-native observability. The transition from Phase 2 to Phase 3 is largely a packaging and deployment change; the core logic remains the same."));

  children.push(heading("15.3 Additional Future Features", HeadingLevel.HEADING_2));
  children.push(bulletItem("Multi-agent workflows: Define DAGs of tasks that span multiple workers with dependencies and conditional branching"));
  children.push(bulletItem("Agent learning: Track operator corrections and use them to improve automated task execution over time"));
  children.push(bulletItem("Cost optimization: Token usage budgets per worker/role with alerts and automatic downscaling"));
  children.push(bulletItem("Multi-tenant support: Organization-level isolation with shared infrastructure"));
  children.push(bulletItem("Plugin marketplace: Community-contributed role definitions, task templates, and dashboard widgets"));
  children.push(bulletItem("IDE deep integration: File-aware operator mode that syncs agent edits with the editor in real time"));

  return children;
}

// ── Assemble Document
const doc = new Document({
  styles: {
    default: {
      document: {
        run: {
          font: { ascii: "Times New Roman", eastAsia: "SimSun" },
          size: 24,
          color: c(P.body),
        },
        paragraph: {
          spacing: { line: 312 },
        },
      },
      heading1: {
        run: {
          font: { ascii: "Times New Roman", eastAsia: "SimHei" },
          size: 32,
          bold: true,
          color: c(P.primary),
        },
        paragraph: { spacing: { before: 480, after: 200, line: 312 } },
      },
      heading2: {
        run: {
          font: { ascii: "Times New Roman", eastAsia: "SimHei" },
          size: 28,
          bold: true,
          color: c(P.primary),
        },
        paragraph: { spacing: { before: 360, after: 160, line: 312 } },
      },
      heading3: {
        run: {
          font: { ascii: "Times New Roman", eastAsia: "SimHei" },
          size: 26,
          bold: true,
          color: c(P.primary),
        },
        paragraph: { spacing: { before: 240, after: 120, line: 312 } },
      },
    },
  },
  sections: [
    // Section 1: Cover
    {
      properties: {
        page: { margin: { top: 0, bottom: 0, left: 0, right: 0 } },
      },
      children: buildCover(),
    },
    // Section 2: TOC
    {
      properties: {
        page: {
          margin: { top: 1440, bottom: 1440, left: 1701, right: 1417 },
          pageNumbers: { start: 1, formatType: NumberFormat.LOWER_ROMAN },
        },
      },
      footers: {
        default: new Footer({
          children: [new Paragraph({
            alignment: AlignmentType.CENTER,
            children: [new TextRun({ children: [PageNumber.CURRENT], size: 18, color: c(P.secondary) })],
          })],
        }),
      },
      children: [
        new Paragraph({
          spacing: { after: 400 },
          children: [new TextRun({ text: "Table of Contents", size: 32, bold: true, color: c(P.primary), font: { ascii: "Times New Roman", eastAsia: "SimHei" } })],
        }),
        new TableOfContents("Table of Contents", {
          hyperlink: true,
          headingStyleRange: "1-3",
        }),
        new Paragraph({ children: [new PageBreak()] }),
      ],
    },
    // Section 3: Body
    {
      properties: {
        page: {
          margin: { top: 1440, bottom: 1440, left: 1701, right: 1417 },
          pageNumbers: { start: 1, formatType: NumberFormat.DECIMAL },
        },
      },
      headers: {
        default: new Header({
          children: [new Paragraph({
            alignment: AlignmentType.RIGHT,
            children: [new TextRun({ text: "Reasonix Fleet Manager - Architecture Proposal", size: 18, color: c(P.secondary), font: { ascii: "Times New Roman", eastAsia: "SimSun" } })],
          })],
        }),
      },
      footers: {
        default: new Footer({
          children: [new Paragraph({
            alignment: AlignmentType.CENTER,
            children: [new TextRun({ children: [PageNumber.CURRENT], size: 18, color: c(P.secondary) })],
          })],
        }),
      },
      children: buildBody(),
    },
  ],
});

// ── Generate
Packer.toBuffer(doc).then(buf => {
  fs.writeFileSync("/home/z/my-project/DeepSeek-Reasonix/docs/Reasonix_Fleet_Manager_Architecture_Proposal.docx", buf);
  console.log("Document generated successfully.");
});
