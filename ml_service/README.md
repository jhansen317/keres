# Keres ML Service

This Python service provides semantic photo search capabilities using OpenAI's CLIP model.

## What is CLIP?

CLIP (Contrastive Language-Image Pre-training) is a neural network trained on 400 million image-text pairs. It can:
- Understand the semantic content of images
- Match natural language descriptions to images
- Enable searches like "beach sunset", "photos of my dog", "food pictures"

## Features

- **Semantic Search**: Find photos using natural language descriptions
- **Embedding Cache**: Photos are indexed once, searches are instant
- **Offline Processing**: All ML runs locally, no cloud APIs needed
- **REST API**: Easy integration with the Go CLI

## Requirements

- Python 3.8+
- 4GB+ RAM (8GB+ recommended for large libraries)
- Optional: CUDA-capable GPU for faster processing

## Installation

1. **Create a virtual environment** (recommended):
   ```bash
   cd ml_service
   python3 -m venv venv
   source venv/bin/activate  # On Windows: venv\Scripts\activate
   ```

2. **Install dependencies**:
   ```bash
   pip install -r requirements.txt
   ```

   First run will download the CLIP model (~350MB).

## Usage

### Start the Service

```bash
python app.py
```

The service will start on `http://localhost:5000`.

### Index Your Photos

Before searching, you need to generate embeddings for your photos:

```bash
# From the main project directory
./keres photos index
```

This will:
- Scan your Photos library
- Generate CLIP embeddings for each photo
- Cache embeddings in `~/.keres/photo_embeddings.db`

**Note**: Indexing can take time:
- ~10,000 photos: 30-60 minutes (CPU) or 5-10 minutes (GPU)
- ~50,000 photos: 2-4 hours (CPU) or 20-30 minutes (GPU)

### Search Photos

Once indexed, search is instant:

```bash
./keres photos search "beach sunset"
./keres photos search "photos of my dog"
./keres photos search "food pictures"
./keres photos search "group photos with people"
```

## API Endpoints

### GET /health
Health check and model status.

```bash
curl http://localhost:5000/health
```

### POST /embed/image
Generate embedding for a single image.

```bash
curl -X POST http://localhost:5000/embed/image \
  -H "Content-Type: application/json" \
  -d '{"image_path": "/path/to/photo.jpg", "uuid": "ABC123"}'
```

### POST /embed/text
Generate embedding for text query.

```bash
curl -X POST http://localhost:5000/embed/text \
  -H "Content-Type: application/json" \
  -d '{"text": "beach sunset"}'
```

### POST /search
Search photos by text description.

```bash
curl -X POST http://localhost:5000/search \
  -H "Content-Type: application/json" \
  -d '{"query": "beach sunset", "limit": 20}'
```

Response:
```json
{
  "query": "beach sunset",
  "total_indexed": 15420,
  "results": [
    {
      "uuid": "ABC123",
      "path": "/path/to/photo.jpg",
      "score": 0.89
    }
  ]
}
```

### POST /index/batch
Index multiple photos at once.

```bash
curl -X POST http://localhost:5000/index/batch \
  -H "Content-Type: application/json" \
  -d '{
    "photos": [
      {"uuid": "ABC123", "path": "/path/to/photo1.jpg"},
      {"uuid": "DEF456", "path": "/path/to/photo2.jpg"}
    ]
  }'
```

### GET /stats
Get indexing statistics.

```bash
curl http://localhost:5000/stats
```

## How It Works

### 1. Indexing Phase

```
Photo → CLIP Vision Encoder → 512-dim Vector → SQLite Database
```

Each photo is converted to a 512-dimensional embedding that captures its semantic content.

### 2. Search Phase

```
Text Query → CLIP Text Encoder → 512-dim Vector
                                      ↓
                        Compare with all photo embeddings
                                      ↓
                          Return most similar photos
```

Text queries are embedded in the same vector space, allowing semantic matching.

### 3. Similarity Calculation

Uses cosine similarity to measure how close a text embedding is to each photo embedding:
- Score 0.8+: Very strong match
- Score 0.6-0.8: Good match
- Score 0.4-0.6: Weak match
- Score <0.4: Poor match

## Performance

**Indexing Speed** (on Apple M1 Pro):
- ~6-8 images/second (CPU)
- ~20-30 images/second (GPU with MPS)

**Search Speed**:
- 10,000 photos: ~50ms
- 50,000 photos: ~200ms
- Instant for practical purposes

**Storage**:
- Each embedding: ~2KB
- 10,000 photos: ~20MB
- 50,000 photos: ~100MB

## Troubleshooting

### "Out of memory" error

Reduce batch size or use CPU instead of GPU:
```python
device = "cpu"  # Force CPU in app.py
```

### Slow indexing

- Use a GPU if available
- Close other applications
- Index in batches during off-hours

### "Model not found" error

The CLIP model should download automatically on first run. If it fails:
```bash
python -c "from transformers import CLIPModel; CLIPModel.from_pretrained('openai/clip-vit-base-patch32')"
```

## Advanced Usage

### Using a Different CLIP Model

Edit `app.py` and change:
```python
model_name = "openai/clip-vit-base-patch32"
```

Options:
- `openai/clip-vit-base-patch32` - Fast, balanced (default)
- `openai/clip-vit-large-patch14` - Slower, more accurate
- `openai/clip-vit-base-patch16` - Middle ground

### Running on GPU

If you have an NVIDIA GPU with CUDA:
```bash
pip install torch torchvision --index-url https://download.pytorch.org/whl/cu121
```

The service will automatically use GPU if available.

### Production Deployment

For production use, consider:
- Using Gunicorn: `gunicorn -w 4 -b 0.0.0.0:5000 app:app`
- Adding authentication
- Rate limiting
- Caching frequently searched queries

## Future Enhancements

- [ ] Batch processing optimization
- [ ] Progressive indexing (index new photos only)
- [ ] Multi-GPU support
- [ ] Face recognition integration
- [ ] Automatic tagging based on content
- [ ] Smart album creation
- [ ] Duplicate detection using perceptual hashing

## License

MIT License - Same as the parent project
