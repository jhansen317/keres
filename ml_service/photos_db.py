"""
Photos.app database reader for macOS.
Reads the SQLite database inside the Photos Library package to discover
photos and resolve their original file paths on disk.

On modern macOS (Ventura+), the main database is Photos.sqlite.
Older versions use photos.db. We try both.
"""

import os
import sqlite3
from dataclasses import dataclass
from typing import List, Optional


PHOTOS_LIBRARY_PATH = os.path.expanduser(
    "~/Pictures/Photos Library.photoslibrary"
)
ORIGINALS_PATH = os.path.join(PHOTOS_LIBRARY_PATH, "originals")

# Modern macOS uses Photos.sqlite; older versions use photos.db
_DB_CANDIDATES = [
    os.path.join(PHOTOS_LIBRARY_PATH, "database/Photos.sqlite"),
    os.path.join(PHOTOS_LIBRARY_PATH, "database/photos.db"),
]


@dataclass
class PhotoRecord:
    uuid: str
    filename: str
    directory: str
    media_type: str  # "Image" or "Video"
    kind: int
    width: int
    height: int

    @property
    def original_path(self) -> str:
        """Resolve the full path to the original file on disk."""
        return os.path.join(ORIGINALS_PATH, self.directory, self.filename)

    @property
    def exists(self) -> bool:
        return os.path.isfile(self.original_path)

    @property
    def is_image(self) -> bool:
        return self.media_type == "Image"


def _find_db_path() -> str:
    """Find the Photos.app database file."""
    for path in _DB_CANDIDATES:
        if os.path.exists(path):
            return path
    raise FileNotFoundError(
        f"Photos database not found in {PHOTOS_LIBRARY_PATH}/database/\n"
        "Make sure Photos.app has been opened and your library is synced."
    )


def get_db_connection() -> sqlite3.Connection:
    """Open a read-only connection to the Photos.app database."""
    db_path = _find_db_path()
    return sqlite3.connect(f"file:{db_path}?mode=ro", uri=True)


def discover_photos(images_only: bool = True) -> List[PhotoRecord]:
    """
    Read the Photos.app database and return all non-trashed photo records.
    Only returns photos whose originals exist on disk (downloaded from iCloud).
    """
    conn = get_db_connection()
    cursor = conn.cursor()

    kind_filter = "AND ZASSET.ZKIND = 0" if images_only else ""

    query = f"""
        SELECT
            ZASSET.ZUUID,
            ZASSET.ZFILENAME,
            ZASSET.ZDIRECTORY,
            CASE WHEN ZASSET.ZKIND = 0 THEN 'Image' ELSE 'Video' END,
            ZASSET.ZKIND,
            COALESCE(ZASSET.ZWIDTH, 0),
            COALESCE(ZASSET.ZHEIGHT, 0)
        FROM ZASSET
        WHERE ZASSET.ZTRASHEDSTATE = 0
        {kind_filter}
    """

    try:
        cursor.execute(query)
    except sqlite3.OperationalError as e:
        conn.close()
        raise FileNotFoundError(
            "Photos library database exists but has no photo data.\n"
            f"({e})\n"
            "Open Photos.app and wait for your library to sync, then try again."
        )

    photos = []
    for row in cursor.fetchall():
        photos.append(PhotoRecord(
            uuid=row[0],
            filename=row[1],
            directory=row[2] or "",
            media_type=row[3],
            kind=row[4],
            width=row[5],
            height=row[6],
        ))

    conn.close()
    return photos


def get_photo_count() -> int:
    """Get total number of non-trashed images in the library."""
    conn = get_db_connection()
    cursor = conn.cursor()
    try:
        cursor.execute(
            "SELECT COUNT(*) FROM ZASSET WHERE ZTRASHEDSTATE = 0 AND ZKIND = 0"
        )
    except sqlite3.OperationalError:
        conn.close()
        raise FileNotFoundError(
            "Photos library database exists but has no photo data.\n"
            "Open Photos.app and wait for your library to sync, then try again."
        )
    count = cursor.fetchone()[0]
    conn.close()
    return count
