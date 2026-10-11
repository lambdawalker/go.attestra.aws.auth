# Documentation maintenance

Consumer integration starts at [docs/agents/index.md](docs/agents/index.md). Source behavior outranks prose; central requirements remain normative where implementation is incomplete. Do not silently rewrite a requirement to match a defect.

Read [documentation maintenance](sites/README.md) before editing site tooling. Keep raw Markdown, HTML and translations in the same source scope. Run the documented link/build checks. Never run AWS deployment, teardown or package publication to validate documentation. Do not commit generated `sites/dist`, local receipts, credentials or unreviewed screenshots.
