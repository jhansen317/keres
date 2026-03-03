#!/usr/bin/env python3
"""
Keres ML Service
Provides semantic photo search using CLIP embeddings.
Reads the macOS Photos.app library and indexes all photos for text-based search.
"""

import os
import sqlite3
import threading
import time
from typing import List, Dict, Optional

import numpy as np
from flask import Flask, request, jsonify
from PIL import Image
from pillow_heif import register_heif_opener
register_heif_opener()
import torch
from transformers import CLIPProcessor, CLIPModel

from photos_db import discover_photos, get_photo_count, PhotoRecord
from video import extract_frames, cleanup_frames

app = Flask(__name__)

# Global model and processor
clip_model: Optional[CLIPModel] = None
clip_processor: Optional[CLIPProcessor] = None
device = "mps" if torch.backends.mps.is_available() else (
    "cuda" if torch.cuda.is_available() else "cpu"
)

# Database path for embeddings cache
DB_PATH = os.path.expanduser("~/.keres/photo_embeddings.db")

# Indexing state (shared across threads)
index_state = {
    "running": False,
    "total": 0,
    "indexed": 0,
    "skipped": 0,
    "failed": 0,
    "started_at": None,
    "finished_at": None,
    "current_file": "",
    "error": None,
}
index_lock = threading.Lock()


def init_clip_model():
    """Initialize CLIP model and processor."""
    global clip_model, clip_processor

    print("Loading CLIP model...")
    print(f"Using device: {device}")

    model_name = "openai/clip-vit-base-patch32"

    clip_model = CLIPModel.from_pretrained(model_name).to(device)
    clip_processor = CLIPProcessor.from_pretrained(model_name)

    clip_model.eval()
    print("CLIP model loaded successfully")


def init_database():
    """Initialize SQLite database for storing embeddings."""
    os.makedirs(os.path.dirname(DB_PATH), exist_ok=True)

    conn = sqlite3.connect(DB_PATH)
    cursor = conn.cursor()

    cursor.execute("""
        CREATE TABLE IF NOT EXISTS embeddings (
            photo_uuid TEXT PRIMARY KEY,
            photo_path TEXT NOT NULL,
            embedding BLOB NOT NULL,
            indexed_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
        )
    """)

    cursor.execute("""
        CREATE INDEX IF NOT EXISTS idx_photo_path ON embeddings(photo_path)
    """)

    conn.commit()
    conn.close()
    print(f"Database initialized at {DB_PATH}")


def _to_tensor(features) -> torch.Tensor:
    """Extract tensor from model output (handles both old and new transformers API)."""
    if isinstance(features, torch.Tensor):
        return features
    # transformers 5.x returns BaseModelOutputWithPooling
    if hasattr(features, "pooler_output") and features.pooler_output is not None:
        return features.pooler_output
    if hasattr(features, "last_hidden_state"):
        return features.last_hidden_state[:, 0, :]
    raise TypeError(f"Unexpected feature type: {type(features)}")


@torch.no_grad()
def generate_image_embedding(image_path: str) -> Optional[np.ndarray]:
    """Generate CLIP embedding for an image."""
    try:
        image = Image.open(image_path).convert("RGB")
        inputs = clip_processor(images=image, return_tensors="pt").to(device)
        image_features = _to_tensor(clip_model.get_image_features(**inputs))
        image_features = image_features / image_features.norm(dim=-1, keepdim=True)
        return image_features.cpu().numpy()[0]
    except Exception as e:
        print(f"Error processing {image_path}: {e}")
        return None


@torch.no_grad()
def generate_text_embedding(text: str) -> np.ndarray:
    """Generate CLIP embedding for text."""
    inputs = clip_processor(text=[text], return_tensors="pt", padding=True).to(device)
    text_features = _to_tensor(clip_model.get_text_features(**inputs))
    text_features = text_features / text_features.norm(dim=-1, keepdim=True)
    return text_features.cpu().numpy()[0]


def cosine_similarity(a: np.ndarray, b: np.ndarray) -> float:
    """Compute cosine similarity between two normalized vectors."""
    return float(np.dot(a, b))


def store_embedding(photo_uuid: str, photo_path: str, embedding: np.ndarray):
    """Store embedding in database."""
    conn = sqlite3.connect(DB_PATH)
    cursor = conn.cursor()
    embedding_bytes = embedding.tobytes()
    cursor.execute(
        "INSERT OR REPLACE INTO embeddings (photo_uuid, photo_path, embedding) VALUES (?, ?, ?)",
        (photo_uuid, photo_path, embedding_bytes),
    )
    conn.commit()
    conn.close()


def store_embeddings_batch(records: List[tuple]):
    """Store multiple embeddings in a single transaction."""
    conn = sqlite3.connect(DB_PATH)
    cursor = conn.cursor()
    cursor.executemany(
        "INSERT OR REPLACE INTO embeddings (photo_uuid, photo_path, embedding) VALUES (?, ?, ?)",
        records,
    )
    conn.commit()
    conn.close()


def get_indexed_uuids() -> set:
    """Get the set of already-indexed photo UUIDs."""
    conn = sqlite3.connect(DB_PATH)
    cursor = conn.cursor()
    cursor.execute("SELECT photo_uuid FROM embeddings")
    uuids = {row[0] for row in cursor.fetchall()}
    conn.close()
    return uuids


def get_all_embeddings() -> List[Dict]:
    """Retrieve all embeddings from database."""
    conn = sqlite3.connect(DB_PATH)
    cursor = conn.cursor()
    cursor.execute("SELECT photo_uuid, photo_path, embedding FROM embeddings")
    rows = cursor.fetchall()

    embeddings = []
    for uuid, path, emb_bytes in rows:
        embedding = np.frombuffer(emb_bytes, dtype=np.float32)
        embeddings.append({"uuid": uuid, "path": path, "embedding": embedding})

    conn.close()
    return embeddings


def _index_image(photo: PhotoRecord, batch_records: list):
    """Index a single image. Returns True on success."""
    embedding = generate_image_embedding(photo.original_path)
    if embedding is not None:
        batch_records.append(
            (photo.uuid, photo.original_path, embedding.tobytes())
        )
        return True
    return False


def _index_video(photo: PhotoRecord, batch_records: list, max_frames: int):
    """Extract frames from a video and index each one. Returns (indexed, failed)."""
    frame_paths = extract_frames(photo.original_path, max_frames=max_frames)
    if not frame_paths:
        return 0, 1

    indexed = 0
    failed = 0
    for i, frame_path in enumerate(frame_paths):
        frame_uuid = f"{photo.uuid}_frame_{i}"
        embedding = generate_image_embedding(frame_path)
        if embedding is not None:
            batch_records.append(
                (frame_uuid, photo.original_path, embedding.tobytes())
            )
            indexed += 1
        else:
            failed += 1

    cleanup_frames(frame_paths)
    return indexed, failed


def _run_index_all(skip_indexed: bool, batch_size: int, video_frames: int):
    """Background worker that indexes all photos and videos from the library."""
    global index_state

    try:
        # Discover both images and videos
        all_media = discover_photos(images_only=False)

        # Filter to only media whose originals exist on disk
        all_media = [p for p in all_media if p.exists]

        if skip_indexed:
            already_indexed = get_indexed_uuids()
            # For images, skip if uuid indexed. For videos, skip if any frame indexed.
            filtered = []
            for p in all_media:
                if p.is_image:
                    if p.uuid not in already_indexed:
                        filtered.append(p)
                else:
                    if f"{p.uuid}_frame_0" not in already_indexed:
                        filtered.append(p)
            all_media = filtered

        with index_lock:
            index_state["total"] = len(all_media)

        if not all_media:
            with index_lock:
                index_state["running"] = False
                index_state["finished_at"] = time.time()
            return

        batch_records = []

        for item in all_media:
            with index_lock:
                if not index_state["running"]:
                    break
                index_state["current_file"] = item.filename

            if item.is_image:
                if _index_image(item, batch_records):
                    with index_lock:
                        index_state["indexed"] += 1
                else:
                    with index_lock:
                        index_state["failed"] += 1
            else:
                ok, bad = _index_video(item, batch_records, video_frames)
                with index_lock:
                    index_state["indexed"] += (1 if ok > 0 else 0)
                    index_state["failed"] += (1 if ok == 0 else 0)

            if len(batch_records) >= batch_size:
                store_embeddings_batch(batch_records)
                batch_records = []

        if batch_records:
            store_embeddings_batch(batch_records)

    except Exception as e:
        with index_lock:
            index_state["error"] = str(e)
        print(f"Indexing error: {e}")

    finally:
        with index_lock:
            index_state["running"] = False
            index_state["finished_at"] = time.time()
            index_state["current_file"] = ""


# ── API Endpoints ──


@app.route("/health", methods=["GET"])
def health_check():
    return jsonify({
        "status": "healthy",
        "model_loaded": clip_model is not None,
        "device": device,
    })


@app.route("/embed/image", methods=["POST"])
def embed_image():
    """Generate embedding for a single image."""
    data = request.json
    image_path = data.get("image_path")
    photo_uuid = data.get("uuid", "")

    if not image_path or not os.path.exists(image_path):
        return jsonify({"error": "Invalid image path"}), 400

    embedding = generate_image_embedding(image_path)
    if embedding is None:
        return jsonify({"error": "Failed to generate embedding"}), 500

    if photo_uuid:
        store_embedding(photo_uuid, image_path, embedding)

    return jsonify({
        "uuid": photo_uuid,
        "embedding": embedding.tolist(),
        "cached": photo_uuid != "",
    })


@app.route("/embed/text", methods=["POST"])
def embed_text():
    """Generate embedding for text query."""
    data = request.json
    text = data.get("text")

    if not text:
        return jsonify({"error": "No text provided"}), 400

    embedding = generate_text_embedding(text)
    return jsonify({"text": text, "embedding": embedding.tolist()})


@app.route("/search", methods=["POST"])
def search_photos():
    """Search photos using text query."""
    data = request.json
    query = data.get("query")
    top_k = data.get("limit", 20)

    if not query:
        return jsonify({"error": "No query provided"}), 400

    text_embedding = generate_text_embedding(query)
    all_embeddings = get_all_embeddings()

    if not all_embeddings:
        return jsonify({
            "query": query,
            "results": [],
            "message": "No photos indexed yet. Run 'keres photos index' first.",
        })

    results = []
    for photo in all_embeddings:
        similarity = cosine_similarity(text_embedding, photo["embedding"])
        results.append({
            "uuid": photo["uuid"],
            "path": photo["path"],
            "score": similarity,
        })

    results.sort(key=lambda x: x["score"], reverse=True)

    return jsonify({
        "query": query,
        "total_indexed": len(all_embeddings),
        "results": results[:top_k],
    })


@app.route("/index/batch", methods=["POST"])
def index_batch():
    """Index a batch of photos by explicit path."""
    data = request.json
    photos = data.get("photos", [])

    if not photos:
        return jsonify({"error": "No photos provided"}), 400

    indexed = 0
    failed = 0

    for photo in photos:
        uuid = photo.get("uuid")
        path = photo.get("path")

        if not uuid or not path or not os.path.exists(path):
            failed += 1
            continue

        embedding = generate_image_embedding(path)
        if embedding is not None:
            store_embedding(uuid, path, embedding)
            indexed += 1
        else:
            failed += 1

    return jsonify({"indexed": indexed, "failed": failed, "total": len(photos)})


@app.route("/index/all", methods=["POST"])
def index_all():
    """
    Discover all photos from the macOS Photos library and index them.
    Runs in a background thread so the request returns immediately.

    Body (optional):
      skip_indexed: bool (default true) - skip already-indexed photos
      batch_size: int (default 50) - DB write batch size
    """
    global index_state

    with index_lock:
        if index_state["running"]:
            return jsonify({"error": "Indexing already in progress"}), 409

    data = request.json or {}
    skip_indexed = data.get("skip_indexed", True)
    batch_size = data.get("batch_size", 50)
    video_frames = data.get("video_frames", 4)

    # Discover total count first for immediate feedback
    try:
        total_in_library = get_photo_count()
    except FileNotFoundError as e:
        return jsonify({"error": str(e)}), 400

    already_indexed = len(get_indexed_uuids()) if skip_indexed else 0

    with index_lock:
        index_state = {
            "running": True,
            "total": 0,  # will be set accurately by the worker
            "indexed": 0,
            "skipped": already_indexed if skip_indexed else 0,
            "failed": 0,
            "started_at": time.time(),
            "finished_at": None,
            "current_file": "",
            "error": None,
        }

    thread = threading.Thread(
        target=_run_index_all,
        args=(skip_indexed, batch_size, video_frames),
        daemon=True,
    )
    thread.start()

    return jsonify({
        "status": "started",
        "total_in_library": total_in_library,
        "already_indexed": already_indexed,
        "message": "Indexing started in background. Poll /index/status for progress.",
    })


@app.route("/index/status", methods=["GET"])
def index_status():
    """Get current indexing progress."""
    with index_lock:
        state = dict(index_state)

    elapsed = None
    if state["started_at"]:
        end = state["finished_at"] or time.time()
        elapsed = round(end - state["started_at"], 1)

    rate = None
    if elapsed and elapsed > 0 and state["indexed"] > 0:
        rate = round(state["indexed"] / elapsed, 1)

    return jsonify({
        "running": state["running"],
        "total": state["total"],
        "indexed": state["indexed"],
        "skipped": state["skipped"],
        "failed": state["failed"],
        "current_file": state["current_file"],
        "elapsed_seconds": elapsed,
        "images_per_second": rate,
        "error": state["error"],
    })


@app.route("/index/cancel", methods=["POST"])
def index_cancel():
    """Cancel a running indexing job. Already-indexed photos are kept."""
    with index_lock:
        if not index_state["running"]:
            return jsonify({"error": "No indexing in progress"}), 400
        index_state["running"] = False

    return jsonify({"status": "cancelling"})


@app.route("/stats", methods=["GET"])
def get_stats():
    """Get indexing statistics."""
    conn = sqlite3.connect(DB_PATH)
    cursor = conn.cursor()

    cursor.execute("SELECT COUNT(*) FROM embeddings")
    total_indexed = cursor.fetchone()[0]

    cursor.execute("SELECT MIN(indexed_at), MAX(indexed_at) FROM embeddings")
    first_indexed, last_indexed = cursor.fetchone()

    conn.close()

    try:
        total_in_library = get_photo_count()
    except Exception:
        total_in_library = None

    return jsonify({
        "total_indexed": total_indexed,
        "total_in_library": total_in_library,
        "first_indexed": first_indexed,
        "last_indexed": last_indexed,
        "database_path": DB_PATH,
    })


@app.route("/debug/media_counts", methods=["GET"])
def debug_media_counts():
    """Temporary debug endpoint to check media discovery."""
    from photos_db import get_db_connection
    conn = get_db_connection()
    cur = conn.cursor()
    cur.execute("SELECT ZKIND, COUNT(*) FROM ZASSET WHERE ZTRASHEDSTATE = 0 GROUP BY ZKIND")
    kinds = {str(row[0]): row[1] for row in cur.fetchall()}
    conn.close()

    all_media = discover_photos(images_only=False)
    images = [p for p in all_media if p.is_image]
    videos = [p for p in all_media if not p.is_image]
    images_on_disk = [p for p in images if p.exists]
    videos_on_disk = [p for p in videos if p.exists]

    return jsonify({
        "zkind_counts": kinds,
        "total_discovered": len(all_media),
        "images": len(images),
        "videos": len(videos),
        "images_on_disk": len(images_on_disk),
        "videos_on_disk": len(videos_on_disk),
        "sample_videos": [{"uuid": v.uuid, "file": v.filename, "path": v.original_path, "exists": v.exists} for v in videos[:5]],
    })


if __name__ == "__main__":
    print("Keres ML Service")
    print("=" * 50)

    init_database()
    init_clip_model()

    port = int(os.environ.get("PORT", 5001))

    print(f"\nStarting server on http://localhost:{port}")
    print("\nEndpoints:")
    print("  GET  /health          - Health check")
    print("  POST /embed/image     - Generate image embedding")
    print("  POST /embed/text      - Generate text embedding")
    print("  POST /search          - Search photos by text")
    print("  POST /index/batch     - Index photos by path")
    print("  POST /index/all       - Index entire Photos library")
    print("  GET  /index/status    - Indexing progress")
    print("  POST /index/cancel    - Cancel indexing")
    print("  GET  /stats           - Indexing statistics")

    app.run(host="0.0.0.0", port=port, debug=False)
