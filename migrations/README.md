Database schema v0 is embedded from `internal/store/schema.sql` using Go embed.
It is part of the executable; runtime migration does not load mutable SQL files.

`scp init` creates the tables and schema-version row in one transaction. Repeating
init is idempotent. Unknown versions, missing tables on non-init commands, invalid
JSON, dangling references and violated conservation rules fail closed.

`core_state` holds the concrete typed Core state. Every update takes an immediate
SQLite transaction, validates references, conservation and immutable history, and
commits the complete change atomically. `runtime_blockers` and
`repo_update_journal` are separately inspectable durable runtime tables.
Foreign keys are enabled and checked; semantic references inside the typed state
are enforced by `store.Validate`, not by JSON text alone. This is a single concrete
SQLite implementation, with no storage interface or event-sourcing framework.
