# Documentation maintenance

This repository owns its Markdown and a small Python-Markdown static renderer. The backend is a service/operator tool; the central repository is architecture. Neither is presented as a newly published SDK. The site does not add runtime dependencies to Go or execute deployment commands.

## Build and preview

From the repository root, using Python 3.12:

```sh
python -m venv .venv-docs
.venv-docs/bin/pip install -r sites/requirements.txt
.venv-docs/bin/python sites/test_build.py
.venv-docs/bin/python sites/build.py
```

On Windows use `.venv-docs\Scripts\python.exe` and `.venv-docs\Scripts\pip.exe` instead. The output is `sites/dist/`; every build clears it. For local project-subpath preview, serve a parent folder with `sites/dist` mounted at `/go.attestra.aws.auth/`. The generated site uses that base path deliberately, not `/`.

## Hosting

GitHub Pages was disabled at the audit. The **Documentation** action builds/checks on pull requests and main pushes and uploads a browsable Pages artifact. To publish, set repository Settings → Pages → Source to **GitHub Actions**, then create repository variable `DOCS_PAGES_ENABLED=true` and run **Documentation** manually. Only the trusted main deployment job has Pages/id-token write permissions; pull requests build with read-only permissions. The intended URL is `https://lambdawalker.github.io/go.attestra.aws.auth/`; it is not advertised as live until hosting succeeds. This workflow cannot deploy AWS or publish packages.

Deployment has a fixed concurrency group and checks that its SHA is still main before publishing. Rerun documentation independently after a hosting failure. Cross-repository commits are not atomic: backend guides publish first, central catalog second; use repository/raw links until both human sites are enabled.

## Ownership, versions and translations

Canonical guides remain in their existing Markdown locations. Focused `docs/agents/` guides are rendered for humans as well as raw Markdown. Existing detailed runbooks/specifications remain authoritative; no duplicated manual copies are generated in source. The site includes search, keyboard navigation, code copying, responsive tables and a language switch that retains the page and `main` scope.

`versions.json` currently advertises only development/source docs: no fabricated release history. Before introducing a release, add immutable source/documentation identity and confirmed publication facts, then implement full snapshot rendering and tests. The builder deliberately rejects nonempty release catalogs until that exists. It never silently renders current source as an old release.

Spanish integration guides live in `docs/es/agents/`. `translations.json` binds each translated page to SHA-256 of its canonical English source. Review the translation and preserve executable code fences before intentionally updating the hash. Missing/stale translations visibly show English from the same current documentation snapshot; they never jump to a different API version. Detailed legacy runbooks and design specifications currently use this explicit fallback. This is partial localization, not a claim that every historical page has been translated.

Stable machine entry: `/agents/index.md`; version/locale raw trees: `/raw/en/main/` and `/raw/es/main/`. `llms.txt` and `versions.json` provide discovery without JavaScript. Raw transformations preserve fenced examples and keep Markdown links raw; source-code links deliberately leave the docs tree at the reviewed source ref. Only known Markdown paths and public images are emitted. Prototype HTML remains a GitHub source link, never executes in the site origin.

## Validation and evidence

The builder checks local file targets, generated HTML links/anchors, project base paths and image hashes. Tests exercise raw-link transformation and scope preservation. A documentation build does not prove source compilation, email delivery, device behavior or cloud readiness. Record those checks separately with the source ref. Mermaid fences remain readable diagram source; their GitHub source pages provide diagram rendering. Backend screenshots are unnecessary for a nonvisual service. Central historical prototype images retain explicit provenance limitations; do not call them current application captures.

When changing guides, build from a clean dependency install, inspect desktop/narrow layouts, check reciprocal repository links and update feature coverage. Do not run deployment or teardown to validate prose. External permissions, missing partner docs and hosting setup must be reported, not silently treated as passing links.
