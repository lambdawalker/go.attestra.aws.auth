"""Validate deployment packages before Pulumi uploads them to Lambda."""

from pathlib import Path
from struct import unpack_from
from zipfile import ZipFile


for name in ("signup", "resend", "confirm", "challenge", "passkeyoptions", "passkeycomplete"):
    archive = Path(__file__).resolve().parents[1] / "dist" / f"{name}.zip"
    with ZipFile(archive) as package:
        entries = package.infolist()
        assert len(entries) == 1 and entries[0].filename == "bootstrap", archive
        bootstrap = entries[0]
        assert bootstrap.external_attr >> 16 & 0o111, f"{archive}: bootstrap is not executable"
        with package.open(bootstrap) as stream:
            header = stream.read(20)
        assert header[:4] == b"\x7fELF" and unpack_from("<H", header, 18)[0] == 183, (
            f"{archive}: bootstrap must be an ARM64 Linux ELF executable"
        )
    print(f"Valid Lambda archive: {archive}")
