# Documentation audit — 2026-10-11

This update follows the system-integrated, standalone-library and versioned-multilingual documentation prompts in `lambdawalker/agents.prompts`. It does not deploy application infrastructure or publish a package.

## Verified locally

- Site renderer tests: four passing in each repository.
- Both complete site builds: local document links, generated HTML links and heading anchors, project subpaths and available image provenance hashes checked.
- Browser checks: English and Spanish guide entry pages at 1440px and 390px widths; one page heading, working search and locale links, no page-width overflow. Desktop and mobile screenshots visually inspected.
- Reciprocal documentation/source file targets checked against the local companion repositories. GitHub issue links are separate references, not local file targets.
- Backend suites passed: `go test ./api ./email ./passkey ./signin ./captureapi ./registry`, `go -C tools/deploy test ./...`, and `go -C infra-index test ./...`.

## Remaining scope

GitHub Pages is disabled until the owner enables Actions-based Pages and sets `DOCS_PAGES_ENABLED=true`; see [hosting instructions](../sites/README.md#hosting). Buildable sites are not evidence of live hosting.

Six integration guides per repository have Spanish translations bound to English source hashes. Other pages explicitly fall back to English in the same source scope. There are no confirmed releases or immutable release archives; the renderer refuses release entries until snapshot support is implemented.

Android examples were checked against source, not built or exercised on a device. No live AWS deploy/teardown, SES delivery, real ID upload or end-to-end identity test was performed. Historical prototype screenshots are retained with their provenance limitations; no current-app captures were fabricated. Mermaid remains readable source in this initial renderer, with rendered diagrams available through GitHub.
