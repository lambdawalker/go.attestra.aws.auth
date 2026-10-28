# Deployment Startup Menu

Approved behavior: first-time or incomplete local setup launches the complete wizard; subsequent parameter-free setup/deploy launchers ask whether to deploy both, shared index/admin only, or backend only. The backend stack defaults to dev and remains editable. Explicit operations bypass automatic startup selection.

Implementation:
- Read existing repository-bound bootstrap completion records without tying UI routing to the Git commit; actual operations continue verifying live state.
- Ignore only launcher-injected repo-root/bootstrap flags when deciding whether the user supplied an operation.
- Run shared before backend for both, reuse AWS authentication with an account check, and keep state passphrases separate.
- Preserve cancellation and errors; fail on corrupted receipts; never deploy backend after a shared failure.
- Verify first run, partial setup, completed setup, wrong repository, explicit flags, cancellation, corrupt records and operation ordering. Run deployment tests/vet and documentation checks before publishing.
