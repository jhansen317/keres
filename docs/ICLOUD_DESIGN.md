# iCloud Integration Design Document

## iCloud Storage Cleanup - The Challenge

Unlike Google, Apple doesn't provide a public REST API for iCloud. Here are our options:

### Option 1: iCloud Drive via Filesystem (✓ Implemented)
- **Access:** `~/Library/Mobile Documents/com~apple~CloudDocs/`
- **Pros:** Direct filesystem access, no auth needed (already logged in)
- **Cons:** Only works on macOS, limited to iCloud Drive files
- **Use cases:** Find large files, duplicates, old files in iCloud Drive

### Option 2: Photos Library via SQLite (✓ Implemented)
- **Access:** `~/Pictures/Photos Library.photoslibrary/database/photos.db`
- **Pros:** Rich metadata, fast queries, complete library access
- **Cons:** Read-only (safe!), macOS only, private schema (can change)
- **Use cases:** Find large photos/videos, duplicates, analyze storage

### Option 3: CloudKit API (❌ Limited)
- **Access:** Apple's CloudKit framework
- **Pros:** Official API
- **Cons:** Only for app-specific data, not general iCloud storage
- **Verdict:** Not useful for storage cleanup

### Option 4: PyiCloud (⚠️ Reverse-Engineered)
- **Access:** Python library that mimics iCloud web interface
- **Pros:** Can access more iCloud data remotely
- **Cons:** Unofficial, fragile, 2FA complications, violates TOS
- **Verdict:** Risky for a cleanup tool

## Photo Semantic Search - The Vision

**Goal:** Match photos against descriptive phrases like:
- "beach sunset"
- "photos of my dog"
- "group photos with more than 3 people"
- "food pictures"
- "screenshots"

### How It Works

This requires **semantic similarity** between images and text:

```
Text: "beach sunset" → [0.23, 0.89, -0.12, ...] (embedding vector)
                              ↓ compute similarity
Image: photo.jpg    → [0.25, 0.87, -0.10, ...] (embedding vector)
                              ↓
                    Similarity Score: 0.94 (high match!)
```

### The Technology: CLIP

**CLIP** (Contrastive Language-Image Pre-training) by OpenAI is perfect for this:
- Trained on 400M image-text pairs
- Understands semantic relationships between images and text
- Can match "a photo of a cat" to actual cat photos
- Open source, runs locally

### Architecture Options

#### Option A: Python ML Service + Go CLI (Recommended)
```
┌─────────────┐         ┌──────────────────┐         ┌─────────────┐
│   Go CLI    │ ──HTTP─→│  Python Service  │ ──────→ │ CLIP Model  │
│ (User UI)   │ ←─JSON─→│  (Flask/FastAPI) │         │  (Local)    │
└─────────────┘         └──────────────────┘         └─────────────┘
                                 ↓
                        ┌──────────────────┐
                        │  Vector Storage  │
                        │  (SQLite + FTS)  │
                        └──────────────────┘
```

**Pros:**
- Go handles CLI, concurrency, file operations
- Python handles ML (PyTorch, transformers, CLIP)
- Best of both worlds
- Can run offline

**Cons:**
- Requires Python runtime
- Two processes to manage

#### Option B: Cloud API (OpenAI CLIP API)
```
┌─────────────┐         ┌──────────────────┐
│   Go CLI    │ ──────→ │   OpenAI API     │
│             │ ←─────  │  (CLIP Service)  │
└─────────────┘         └──────────────────┘
```

**Pros:**
- No local ML dependencies
- Always up-to-date models

**Cons:**
- Costs money per API call
- Requires internet
- Privacy concerns (sending photos)

#### Option C: Go + ONNX Runtime
```
┌─────────────┐         ┌──────────────────┐
│   Go CLI    │ ──────→ │  ONNX Runtime    │
│             │         │  (CLIP Model)    │
└─────────────┘         └──────────────────┘
```

**Pros:**
- Single binary
- No Python needed
- Runs offline

**Cons:**
- ONNX Go bindings are immature
- Limited model selection
- Complex setup

### Do We Need a Vector Database?

**Short answer:** Depends on library size.

**Without Vector DB (< 10,000 photos):**
```go
// Simple in-memory search
func findSimilar(queryVector []float64, imageVectors []ImageVector, topK int) []Match {
    scores := make([]Match, len(imageVectors))
    for i, img := range imageVectors {
        scores[i] = Match{
            ImagePath: img.Path,
            Score: cosineSimilarity(queryVector, img.Vector),
        }
    }
    sort.Slice(scores, func(i, j int) bool { return scores[i].Score > scores[j].Score })
    return scores[:topK]
}
```

**With Vector DB (> 10,000 photos):**
- **FAISS** (Facebook AI Similarity Search) - Fast, C++, Python bindings
- **SQLite-VSS** - Vector similarity search in SQLite
- **Chroma** - Lightweight embedding database
- **Qdrant** - Rust-based vector search engine

For most personal photo libraries (10k-50k photos), **SQLite + simple vector search** is plenty fast.

## Implementation Plan

### Phase 1: iCloud Drive Cleanup ✓
- Find large files
- Find duplicates by hash
- Find old files
- Storage usage analysis

### Phase 2: Photos Library Analysis ✓
- Parse Photos.app database
- Find largest photos/videos
- Identify duplicates
- Storage breakdown by type/year

### Phase 3: Semantic Photo Search (Prototype)
- Python service with CLIP
- REST API for embedding generation
- Go CLI integration
- SQLite vector storage
- Search commands:
  ```bash
  keres photos index           # Generate embeddings
  keres photos search "beach"  # Find matching photos
  keres photos tag "my dog" --add-tag "Rocky"  # Auto-tag
  ```

### Phase 4: Advanced Features
- Smart albums based on descriptions
- Automatic organization
- Face clustering (macOS Vision framework)
- Duplicate photo detection (perceptual hashing)

## Native macOS Capabilities

**Vision Framework** (available via Objective-C bridge):
- Scene classification (beach, sunset, food, etc.)
- Object detection
- Face detection and recognition
- Text recognition (OCR)
- Image similarity

**Core ML:**
- Can run custom models
- CLIP models can be converted to Core ML format
- Runs on Neural Engine (very fast on M-series Macs)

**Photos Framework:**
- Can read/write photos
- Access metadata
- But requires entitlements and user permission

**Verdict:** Vision framework is great for pre-defined categories, but CLIP is better for arbitrary natural language queries.

## Storage Considerations

**Photo Embeddings Storage:**
- Each photo = 512 floats (CLIP) = 2KB
- 10,000 photos = 20MB of embeddings
- 50,000 photos = 100MB of embeddings
- **Conclusion:** Easy to store, no special database needed for most users

## Recommended Architecture

```
keres (Go binary)
    ├── iCloud Drive cleanup (filesystem)
    ├── Photos library analysis (SQLite queries)
    └── Semantic search (calls Python service)

keres_ml (Python service)
    ├── CLIP model (runs locally)
    ├── Embedding cache (SQLite)
    └── REST API (Flask/FastAPI)
```

This gives us:
- Fast Go CLI for everyday operations
- Powerful Python ML for semantic search
- All processing stays local (privacy)
- Works offline
- Professional UX
