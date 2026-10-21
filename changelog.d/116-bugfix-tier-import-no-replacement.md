`rustfs_tier` can be imported without planning to replace the tier. `Read` now fills `tier_type`
from the server's tier listing, so importing a tier that already exists no longer leaves the
required `tier_type` empty and triggers its `RequiresReplace` plan. If that listing cannot report
the type, the import fails with a diagnostic instead of silently planning a replacement.
`config_json` still has to be supplied by the configuration, because the server redacts backend
credentials from that listing; the resulting plan updates the tier in place instead of deleting
and recreating it.
