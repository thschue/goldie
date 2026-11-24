---
name: Using the Harbor database
keywords: [database, postgres, migrations, connection pool, schema]
---
Every Harbor service owns one PostgreSQL schema and never reads another service's tables.

- Run schema changes as numbered migrations in the service repository, never by hand.
- Keep migrations backwards compatible for one release: add a column first, remove it later.
- Open one connection pool per process and cap it at 10 connections.
- Set a statement timeout of 5 seconds on every connection.
