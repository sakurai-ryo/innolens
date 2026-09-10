# innolens

## Overview

innolens is a terminal UI for looking inside InnoDB tablespaces: the `.ibd` files
of a MySQL datadir, the redo log, and the undo tablespaces. It decodes pages down
to individual bytes and shows what each one means, so you can see how a row, a
statement, or a version chain is actually stored on disk.

It targets MySQL 8.0.30 and later, and 8.4. It reads uncompressed, unencrypted,
16KB-page, file-per-table tablespaces. Files are opened read-only, so it is safe
to point at a running server's datadir.

A true-color terminal with a Nerd Font is assumed. Set `INNOLENS_ICONS=0` to fall
back to plain Unicode symbols if glyphs render as tofu.

## Installation

```sh
go install github.com/sakurai-ryo/innolens/cmd/innolens@latest
innolens /var/lib/mysql
```

A second datadir can be given as a baseline: `innolens after before` diffs every
page against the same page in `before`.

## Features

- Browse every table in a datadir, then walk each index B+tree and the remaining
  pages of its tablespace.
- Filter the datadir tree with `/`, by schema, by table, or by `schema.table`.
- Page detail view: hex dump, a region minimap of the whole page, and a tree of
  structural annotations that highlights the bytes it describes.
- Records decoded into column values using the table's SDI, including layouts
  rewritten by instant `ADD`/`DROP COLUMN`.
- Redo log browser: everything after the last checkpoint as an mtr → record tree.
- Redo replay: `n` and `p` step the records of a page onto its bytes the way
  recovery would, one at a time, with the changed bytes underlined. A record the
  page has already seen is skipped, as recovery skips it.
- Record images logged by `MLOG_REC_INSERT` rebuilt and decoded into columns.
- Two-way jumps between a redo record and the page it modified.
- Version chain walking: follow a row's `DB_ROLL_PTR` into the undo record holding
  its previous version, and keep going from there.
- A `versions` section under each record rebuilds those previous versions into
  full rows. `v` sets a read view, marking the version that transaction reads.
- Key lookup with `f`: the B+tree descent drawn as one row per page read, root to
  leaf, ending on the record itself.
- A `clustered index` link under every secondary index leaf record: it shows the
  primary key the record stores, and `enter` looks that key up in the clustered
  index as the descent above.
- Undo tablespaces browsable on their own.
- Reload with `r`, which underlines the bytes that changed since the last read and
  adds a `changes` section to the annotation tree.
- Context help with `?`, explaining the item under the cursor.

## Recording a scenario

`scripts/scenario.sh [-v 80|84] [scenario.sql] [outdir]` runs a scenario against
a throwaway MySQL in Docker and keeps two snapshots of its datadir: `before`,
flushed to disk, and `after`, taken from a killed server so the redo log still
holds what the statement wrote. The scenario file is split at a line reading
`-- step`.

Opening the pair with `innolens outdir/after outdir/before` shows what reached
disk, what is still only in the redo log, and what replaying it does to the page.
