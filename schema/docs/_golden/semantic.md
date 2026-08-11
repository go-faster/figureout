# Reference

## Configuration

| Name | Type | Required | Default | Values | Constraints | Description |
| --- | --- | --- | --- | --- | --- | --- |
| [`server`](#server) | object | yes |  |  |  | HTTP server settings. |
| [`storage`](#storage-type-s3) | union | yes |  |  |  | Where the service keeps its data. |
| [`sites`](#sites) | list of object | no |  |  |  | Served sites. |
| `level` | string | no | `"info"` | `"debug"`, `"info"` |  | Log verbosity. |
| `tags` | list of string | no | `[]` |  | checked by sorted; at most 8 items | Tags applied to every metric. |
| `retries` | integer | no | `3` (documented) |  |  | Retry budget; unset means the client default. |
| `region` | string | no | `"eu-west-1"` |  |  | Deployment region. |
| `verbose` | boolean | no | `false` |  |  | Log every request. **Deprecated.** set level to debug instead. |

## server

HTTP server settings.

| Name | Type | Required | Default | Constraints | Description |
| --- | --- | --- | --- | --- | --- |
| `address` | string | no | `"127.0.0.1"` | non-empty; matches ^[a-z0-9.]+$ | Listen address. |
| `port` | integer | no | `8080` | at least 1, at most 65535 | Listen port. |
| `timeout` | integer (seconds) | no | `30` | at least 1 | Request timeout. |

### Examples

`server.port`:

```
8080
9090
```

## storage (type: s3)

Where the service keeps its data.

Selected by `type: s3`.

| Name | Type | Required | Values | Constraints | Description |
| --- | --- | --- | --- | --- | --- |
| `type` | string | yes | `s3` |  | Selects this variant. |
| `bucket` | string | yes |  | non-empty |  |

## storage (type: local)

Where the service keeps its data.

Selected by `type: local`.

| Name | Type | Required | Values | Constraints | Description |
| --- | --- | --- | --- | --- | --- |
| `type` | string | yes | `local` |  | Selects this variant. |
| `path` | string | yes |  | non-empty |  |

## sites[]

Served sites.

| Name | Type | Required | Default | Constraints | Description |
| --- | --- | --- | --- | --- | --- |
| `name` | string | yes |  | non-empty |  |
| `max_bytes` | integer | no | `1024` | at most 1048576 |  |
