# w-popularity-parser-stackoverflow

`stackoverflow` parser for [w_popularity](https://github.com/suenot/w-popularity).

**Status:** stub. `FetchChannel` and `FetchRecentPosts` return `shared.ErrNotImplemented`.

## Strategy

- **Primary:** Stack Exchange API
- **Fallback:** none

## Usage

```go
import parser "github.com/suenot/w-popularity-parser-stackoverflow"

p := parser.New(parser.Config{Credential: os.Getenv("CRED")})
snap, err := p.FetchChannel(ctx, handle)
```

## License

MIT
