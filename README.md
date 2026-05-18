# w-popularity-parser-stackoverflow

`stackoverflow` parser for [w_popularity](https://github.com/suenot/w-popularity), powered by the
[Stack Exchange API v2.3](https://api.stackexchange.com/docs).

## Strategy

- **Primary:** Stack Exchange API v2.3 (`site=stackoverflow`). No authentication is required.
  Anonymous quota is 300 requests/day per IP; supplying an app key (`Config.AppKey`) raises
  this to 10000/day.
- **Fallback:** none.

Endpoints used (all GET, JSON):

- `GET /users/{id}?site=stackoverflow&filter=!)RL-JogHwoZuazwo6-n_WuMc`
- `GET /users/{id}/answers?site=stackoverflow&order=desc&sort=creation&pagesize=50`
- `GET /users/{id}/questions?site=stackoverflow&order=desc&sort=creation&pagesize=50`

### Custom user filter

The Stack Exchange `default` filter omits the per-user totals (`view_count`,
`question_count`, `answer_count`, `up_vote_count`, `down_vote_count`). A stable
custom filter that adds those + envelope diagnostics (`backoff`, `quota_*`,
`error_*`) was generated once via `/filters/create` and is hard-coded as
`!)RL-JogHwoZuazwo6-n_WuMc`.

## Handle format

w_popularity stores Stack Overflow handles as `"<userid>/<slug>"`, e.g.
`"937966/eugen-soloviov"`. Only the numeric id is sent to the API; the slug is human-readable
metadata and is ignored for API calls. A bare numeric id (`"937966"`) is also accepted.

## Field mapping

Stack Overflow has no follow graph, so reputation is used as the closest "audience" proxy.

| Snapshot field          | Source                                        |
| ----------------------- | --------------------------------------------- |
| `Followers`             | `reputation`                                  |
| `PostsCount`            | `question_count + answer_count`               |
| `TotalViews`            | `view_count` (profile views)                  |
| `TotalLikes`            | `up_vote_count`                               |
| `TotalComments`         | `0` (not exposed cleanly per user)            |
| `Raw[reputation]`       | `reputation`                                  |
| `Raw[question_count]`   | `question_count`                              |
| `Raw[answer_count]`     | `answer_count`                                |
| `Raw[display_name]`     | `display_name`                                |
| `Raw[quota_remaining]`  | `quota_remaining` (from envelope)             |
| `Raw[backoff]`          | `backoff` (only set when API requests one)    |

## Quota & backoff handling

Every Stack Exchange response includes envelope fields `backoff` and `quota_remaining`. The
parser logs these via `log.Printf`. `error_id=502` ("throttle_violation") maps to
`shared.ErrRateLimited`. `backoff > 0` is non-fatal; the value is also surfaced in `Raw["backoff"]`
so callers can decide to wait before the next call.

## Usage

```go
import parser "github.com/suenot/w-popularity-parser-stackoverflow"

p := parser.New(parser.Config{AppKey: os.Getenv("STACK_APP_KEY")})

snap, err := p.FetchChannel(ctx, "937966/eugen-soloviov")
posts, err := p.FetchRecentPosts(ctx, "937966/eugen-soloviov", time.Now().AddDate(0, -6, 0))
```

## License

MIT
