# pgroll migrations

See [the pgroll guide](../../docs/pgroll-migrations.md) for the pinned tool,
disposable local rehearsal, and application/production compatibility gates.

`rehearsal/` contains synthetic fixture migrations only. They must never be
applied to the application database. No production migration or baseline is
registered here yet; the historical Rust contracts retain their original files
and hashes.

New application migrations need reviewed `up`/`down` expressions, physical and
version-schema compatibility, and the shared deployment owner contract before
they can be used in production. Completing a pgroll migration removes its old
version schema; rollback of that completed migration is unavailable.
