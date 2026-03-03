"""
Video frame extraction using ffmpeg.
Extracts a small number of evenly-spaced frames from a video file
so they can be indexed with CLIP.
"""

import json
import os
import shutil
import subprocess
import tempfile
from typing import List, Optional


def _ffmpeg_available() -> bool:
    return shutil.which("ffmpeg") is not None


def get_video_duration(video_path: str) -> Optional[float]:
    """Get video duration in seconds using ffprobe."""
    try:
        result = subprocess.run(
            [
                "ffprobe",
                "-v", "quiet",
                "-print_format", "json",
                "-show_format",
                video_path,
            ],
            capture_output=True,
            text=True,
            timeout=10,
        )
        info = json.loads(result.stdout)
        return float(info["format"]["duration"])
    except Exception:
        return None


def extract_frames(
    video_path: str,
    max_frames: int = 4,
    min_interval: float = 2.0,
) -> List[str]:
    """
    Extract evenly-spaced frames from a video file.

    Returns a list of temporary JPEG file paths. The caller is responsible
    for deleting them after use.

    Args:
        video_path: Path to the video file.
        max_frames: Maximum number of frames to extract.
        min_interval: Minimum seconds between frames.
    """
    if not _ffmpeg_available():
        return []

    duration = get_video_duration(video_path)
    if duration is None or duration <= 0:
        return []

    # Calculate timestamps for evenly-spaced frames
    if duration < min_interval:
        # Very short video: just grab the midpoint
        timestamps = [duration / 2]
    else:
        # Space frames evenly, but respect min_interval
        n_frames = min(max_frames, max(1, int(duration / min_interval)))
        step = duration / (n_frames + 1)
        timestamps = [step * (i + 1) for i in range(n_frames)]

    tmp_dir = tempfile.mkdtemp(prefix="keres_frames_")
    frame_paths = []

    for i, ts in enumerate(timestamps):
        out_path = os.path.join(tmp_dir, f"frame_{i:03d}.jpg")
        try:
            subprocess.run(
                [
                    "ffmpeg",
                    "-ss", str(ts),
                    "-i", video_path,
                    "-frames:v", "1",
                    "-q:v", "2",
                    "-y",
                    out_path,
                ],
                capture_output=True,
                timeout=15,
            )
            if os.path.isfile(out_path) and os.path.getsize(out_path) > 0:
                frame_paths.append(out_path)
        except Exception:
            continue

    return frame_paths


def cleanup_frames(frame_paths: List[str]):
    """Delete extracted frame files and their parent temp directory."""
    if not frame_paths:
        return
    parent = os.path.dirname(frame_paths[0])
    for p in frame_paths:
        try:
            os.remove(p)
        except OSError:
            pass
    try:
        os.rmdir(parent)
    except OSError:
        pass
