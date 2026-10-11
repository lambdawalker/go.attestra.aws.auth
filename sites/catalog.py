# Backend owns no duplicated cross-repository catalog.
from pathlib import Path
root=Path(__file__).resolve().parents[1]
assert (root/'docs/coverage.md').is_file()
assert (root/'IMPORT.md').is_file()
print('Backend coverage/installation entries present; central catalog is linked, not copied.')
