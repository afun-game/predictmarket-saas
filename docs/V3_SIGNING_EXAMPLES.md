# V3 HMAC signing examples

Sign the exact bytes sent as the request body. For an empty body, sign
`timestamp + "."` with no trailing JSON or newline.

```go
timestamp := strconv.FormatInt(time.Now().Unix(), 10)
mac := hmac.New(sha256.New, []byte(apiSecret))
mac.Write([]byte(timestamp + "."))
mac.Write(body)
signature := hex.EncodeToString(mac.Sum(nil))
```

```python
timestamp = str(int(time.time()))
signature = hmac.new(
    api_secret.encode(),
    timestamp.encode() + b"." + raw_body,
    hashlib.sha256,
).hexdigest()
```

```javascript
const timestamp = Math.floor(Date.now() / 1000).toString();
const signature = crypto
  .createHmac("sha256", apiSecret)
  .update(timestamp + "." + rawBody, "utf8")
  .digest("hex");
```

Send `Authorization: Bearer <api_key>`, `X-PM-Timestamp`, and
`X-PM-Signature`. State-changing requests also send a unique
`Idempotency-Key`; retain the same key when retrying the same operation.
