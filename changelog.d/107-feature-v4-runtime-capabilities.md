The admin API client is now version-aware. `RequestData.Version` selects the
admin namespace (`v3` when empty, so every existing request keeps its current
URL) and the client stores the host root and the admin base separately. The
provider treats a `511 Upgrade Required` answer for an unmatched v4 path as a
negotiation signal rather than a payload, so a pre-v4 server is reported as
"upgrade RustFS or use the v3 route" naming the route and server version, while
`501 NotImplemented` keeps its existing unimplemented-route diagnostic.

New preview data sources read the RustFS 1.0.3 v4 admin API:
`rustfs_runtime_capabilities` exposes the versioned storage-class write
contract, and `rustfs_plugins`, `rustfs_extensions` and
`rustfs_cluster_snapshot` expose their payloads as the server's JSON verbatim
so the provider does not pin a schema upstream has not committed to. No
plugin-instance resource is added: the v4 payload is still new.
