#!/usr/bin/env python3
"""
Space Reclaimer ML Service
Provides semantic photo search using CLIP embeddings
"""

import os
import sqlite3
from pathlib import Path
from typing import List, Dict, Optional
import numpy as np
from flask import Flask, request, jsonify
from PIL import Image
import torch
from transformers import CLIPProcessor, CLIPModel

app = Flask(__name__)

# Global model and processor
clip_model: Optional[CLIPModel] = None
clip_processor: Optional[CLIPProcessor] = None
device = "cuda" if torch.cuda.is_available() else "cpu"

# Database path for embeddings cache
DB_PATH = os.path.expanduser("~/.space_reclaimer/photo_embeddings.db")


def init_clip_model():
    """Initialize CLIP model and processor"""
    global clip_model, clip_processor

    print("Loading CLIP model...")
    print(f"Using device: {device}")

    # Using OpenAI's CLIP model (ViT-B/32 is a good balance of speed and quality)
    model_name = "openai/clip-vit-base-patch32"

    clip_model = CLIPModel.from_pretrained(model_name).to(device)
    clip_processor = CLIPProcessor.from_pretrained(model_name)

    clip_model.eval()  # Set to evaluation mode
    print("✓ CLIP model loaded successfully")


def init_database():
    """Initialize SQLite database for storing embeddings"""
    os.makedirs(os.path.dirname(DB_PATH), exist_ok=True)

    conn = sqlite3.connect(DB_PATH)
    cursor = conn.cursor()

    # Create embeddings table
    cursor.execute("""
        CREATE TABLE IF NOT EXISTS embeddings (
            photo_uuid TEXT PRIMARY KEY,
            photo_path TEXT NOT NULL,
            embedding BLOB NOT NULL,
            indexed_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
        )
    """)

    # Create index for faster lookups
    cursor.execute("""
        CREATE INDEX IF NOT EXISTS idx_photo_path ON embeddings(photo_path)
    """)

    conn.commit()
    conn.close()
    print(f"✓ Database initialized at {DB_PATH}")


@torch.no_grad()
def generate_image_embedding(image_path: str) -> np.ndarray:
    """Generate CLIP embedding for an image"""
    try:
        image = Image.open(image_path).convert("RGB")
        inputs = clip_processor(images=image, return_tensors="pt").to(device)

        image_features = clip_model.get_image_features(**inputs)

        # Normalize embedding
        image_features = image_features / image_features.norm(dim=-1, keepdim=True)

        return image_features.cpu().numpy()[0]
    except Exception as e:
        print(f"Error processing {image_path}: {e}")
        return None


@torch.no_grad()
def generate_text_embedding(text: str) -> np.ndarray:
    """Generate CLIP embedding for text"""
    inputs = clip_processor(text=[text], return_tensors="pt", padding=True).to(device)

    text_features = clip_model.get_text_features(**inputs)

    # Normalize embedding
    text_features = text_features / text_features.norm(dim=-1, keepdim=True)

    return text_features.cpu().numpy()[0]


def cosine_similarity(a: np.ndarray, b: np.ndarray) -> float:
    """Compute cosine similarity between two vectors"""
    return float(np.dot(a, b))


def store_embedding(photo_uuid: str, photo_path: str, embedding: np.ndarray):
    """Store embedding in database"""
    conn = sqlite3.connect(DB_PATH)
    cursor = conn.cursor()

    # Convert numpy array to bytes
    embedding_bytes = embedding.tobytes()

    cursor.execute("""
        INSERT OR REPLACE INTO embeddings (photo_uuid, photo_path, embedding)
        VALUES (?, ?, ?)
    """, (photo_uuid, photo_path, embedding_bytes))

    conn.commit()
    conn.close()


def get_all_embeddings() -> List[Dict]:
    """Retrieve all embeddings from database"""
    conn = sqlite3.connect(DB_PATH)
    cursor = conn.cursor()

    cursor.execute("SELECT photo_uuid, photo_path, embedding FROM embeddings")
    rows = cursor.fetchall()

    embeddings = []
    for uuid, path, emb_bytes in rows:
        embedding = np.frombuffer(emb_bytes, dtype=np.float32)
        embeddings.append({
            "uuid": uuid,
            "path": path,
            "embedding": embedding
        })

    conn.close()
    return embeddings


# API Endpoints

@app.route("/health", methods=["GET"])
def health_check():
    """Health check endpoint"""
    return jsonify({
        "status": "healthy",
        "model_loaded": clip_model is not None,
        "device": device
    })


@app.route("/embed/image", methods=["POST"])
def embed_image():
    """Generate embedding for a single image"""
    data = request.json
    image_path = data.get("image_path")
    photo_uuid = data.get("uuid", "")

    if not image_path or not os.path.exists(image_path):
        return jsonify({"error": "Invalid image path"}), 400

    embedding = generate_image_embedding(image_path)

    if embedding is None:
        return jsonify({"error": "Failed to generate embedding"}), 500

    # Store in database if UUID provided
    if photo_uuid:
        store_embedding(photo_uuid, image_path, embedding)

    return jsonify({
        "uuid": photo_uuid,
        "embedding": embedding.tolist(),
        "cached": photo_uuid != ""
    })


@app.route("/embed/text", methods=["POST"])
def embed_text():
    """Generate embedding for text query"""
    data = request.json
    text = data.get("text")

    if not text:
        return jsonify({"error": "No text provided"}), 400

    embedding = generate_text_embedding(text)

    return jsonify({
        "text": text,
        "embedding": embedding.tolist()
    })


@app.route("/search", methods=["POST"])
def search_photos():
    """Search photos using text query"""
    data = request.json
    query = data.get("query")
    top_k = data.get("limit", 20)

    if not query:
        return jsonify({"error": "No query provided"}), 400

    # Generate text embedding
    text_embedding = generate_text_embedding(query)

    # Get all photo embeddings
    all_embeddings = get_all_embeddings()

    if not all_embeddings:
        return jsonify({
            "query": query,
            "results": [],
            "message": "No photos indexed yet. Run 'photos index' first."
        })

    # Compute similarities
    results = []
    for photo in all_embeddings:
        similarity = cosine_similarity(text_embedding, photo["embedding"])
        results.append({
            "uuid": photo["uuid"],
            "path": photo["path"],
            "score": float(similarity)
        })

    # Sort by similarity
    results.sort(key=lambda x: x["score"], reverse=True)

    # Return top K
    return jsonify({
        "query": query,
        "total_indexed": len(all_embeddings),
        "results": results[:top_k]
    })


@app.route("/index/batch", methods=["POST"])
def index_batch():
    """Index a batch of photos"""
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

    return jsonify({
        "indexed": indexed,
        "failed": failed,
        "total": len(photos)
    })


@app.route("/stats", methods=["GET"])
def get_stats():
    """Get indexing statistics"""
    conn = sqlite3.connect(DB_PATH)
    cursor = conn.cursor()

    cursor.execute("SELECT COUNT(*) FROM embeddings")
    total_indexed = cursor.fetchone()[0]

    cursor.execute("SELECT MIN(indexed_at), MAX(indexed_at) FROM embeddings")
    first_indexed, last_indexed = cursor.fetchone()

    conn.close()

    return jsonify({
        "total_indexed": total_indexed,
        "first_indexed": first_indexed,
        "last_indexed": last_indexed,
        "database_path": DB_PATH
    })


if __name__ == "__main__":
    print("Space Reclaimer ML Service")
    print("=" * 50)

    # Initialize
    init_database()
    init_clip_model()

    print("\nStarting Flask server...")
    print("API available at: http://localhost:5000")
    print("\nEndpoints:")
    print("  GET  /health          - Health check")
    print("  POST /embed/image     - Generate image embedding")
    print("  POST /embed/text      - Generate text embedding")
    print("  POST /search          - Search photos by text")
    print("  POST /index/batch     - Index multiple photos")
    print("  GET  /stats           - Indexing statistics")

    app.run(host="0.0.0.0", port=5000, debug=False)
