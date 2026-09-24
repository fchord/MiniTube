#!/usr/bin/env python3
"""Copy host ffmpeg/ffprobe and recursive ldd deps into dist/ffmpeg-bundle (no apt)."""
import os
import shutil
import subprocess
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
DEST = ROOT / "dist" / "ffmpeg-bundle"
BINS = ["/usr/bin/ffmpeg", "/usr/bin/ffprobe"]
DRI = Path("/usr/lib/x86_64-linux-gnu/dri")
SKIP_PREFIX = ("linux-vdso",)


def ldd(path: str) -> list[str]:
    out = subprocess.check_output(["ldd", path], text=True, stderr=subprocess.STDOUT)
    libs = []
    for line in out.splitlines():
        line = line.strip()
        if any(line.startswith(s) for s in SKIP_PREFIX):
            continue
        if " => " in line:
            parts = line.split(" => ")
            if len(parts) >= 2:
                loc = parts[1].split()[0]
                if loc.startswith("/"):
                    libs.append(loc)
        elif line.startswith("/") and "ld-linux" in line:
            libs.append(line.split()[0])
    return libs


def copy_file(src: str, dest_root: Path) -> None:
    src_p = Path(src)
    if not src_p.exists():
        return
    rel = src_p
    if src_p.is_absolute():
        dest = dest_root / src_p.relative_to("/")
    else:
        dest = dest_root / src_p
    dest.parent.mkdir(parents=True, exist_ok=True)
    if dest.exists():
        return
    shutil.copy2(src_p, dest, follow_symlinks=True)


def main() -> None:
    if DEST.exists():
        shutil.rmtree(DEST)
    DEST.mkdir(parents=True)
    seen: set[str] = set()
    queue = list(BINS)
    while queue:
        path = queue.pop()
        if path in seen:
            continue
        seen.add(path)
        copy_file(path, DEST)
        try:
            for lib in ldd(path):
                if lib not in seen:
                    queue.append(lib)
        except subprocess.CalledProcessError:
            continue
    if DRI.is_dir():
        dest_dri = DEST / "usr/lib/x86_64-linux-gnu/dri"
        dest_dri.mkdir(parents=True, exist_ok=True)
        for name in ("iHD_drv_video.so", "i965_drv_video.so", "iHD_drv_video.so"):
            src = DRI / name
            if src.exists():
                shutil.copy2(src, dest_dri / name, follow_symlinks=True)
        # copy whatever i965/iHD exist
        for src in DRI.glob("*_drv_video.so"):
            shutil.copy2(src, dest_dri / src.name, follow_symlinks=True)
    print(f"bundled {len(seen)} files into {DEST}")


if __name__ == "__main__":
    main()
