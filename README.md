# agentic-pipeline

An event-driven, agentic development and build pipeline. A developer assigns a work item (e.g. a Jira task) and gives guidance. Agents propose an implementation plan, and **the developer approves it**. Agents then write code and tests across one or more repositories, and an event-driven pipeline builds, tests and integration-tests every commit, feeding structured results back to the agents until the change is ready for human review.

Status: **design phase**. Start with [`docs/README.md`](docs/README.md).
