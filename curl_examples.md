# cURL Examples for Receipt Upload

## Upload Receipt Image

Upload a receipt image to the `/receipts/image` endpoint:

```bash
curl -X POST http://localhost:8080/receipts/image \
  -F "image=@/path/to/your/receipt.jpg" \
  -H "Content-Type: multipart/form-data"
```

### Example with specific file:

```bash
# Upload a JPEG image
curl -X POST http://localhost:8080/receipts/image \
  -F "image=@receipt.jpg"

# Upload a PNG image
curl -X POST http://localhost:8080/receipts/image \
  -F "image=@receipt.png"

# Upload with verbose output to see response
curl -v -X POST http://localhost:8080/receipts/image \
  -F "image=@receipt.jpg"

## Upload Receipt with Document AI

Upload a receipt image or PDF to the `/receipts/document-ai` endpoint:

```bash
curl -X POST http://localhost:8080/receipts/document-ai \
  -F "image=@/path/to/your/receipt.jpg"
```

```bash
curl -X POST http://localhost:8080/receipts/document-ai \
  -F "image=@/path/to/your/receipt.pdf"
```
```

### Example for Heroku deployment:

```bash
curl -X POST https://your-app.herokuapp.com/receipts/image \
  -H "X-Device-Token: $DEVICE_TOKEN" \
  -F "image=@receipt.jpg"
```

### Expected Response:

```json
{
  "receipt_id": "01ARZ3NDEKTSV4RRFFQ69G5FAV",
  "items": [
    { "id": "01ARZ3NDEKTSV4RRFFQ69G5FAW", "name": "Margarita", "amount": { "amount": 12.0, "currency": "USD" } }
  ]
}
```

## Manual Receipt Entry (Existing Endpoint)

Add a receipt manually with items:

```bash
curl -X POST http://localhost:8080/receipts \
  -H "Content-Type: application/json" \
  -d '{
    "items": [
      {
        "name": "Coffee",
        "quantity": 2,
        "price_per_item": 3.50
      },
      {
        "name": "Sandwich",
        "quantity": 1,
        "total_price": 8.99
      }
    ]
  }'
```
