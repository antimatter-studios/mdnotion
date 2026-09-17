# mdnotion

A generic Notion markdown importer: publishes markdown files into Notion pages
from the command line — one page at a time, or whole sets of documents at
once. Pages are addressed by title paths (or page IDs), so nothing about your
Notion hierarchy is hardcoded anywhere.

## Concepts

- **Page reference**: every flag or manifest field that names a page accepts
  either a page **ID** (32 hex characters, what Share → Copy link gives you)
  or a **title path** like `"Engineering Wiki/Docs/Setup"`, resolved at
  runtime by searching for the first segment and walking children for the
  rest. Use `\/` for a literal slash inside a title
  (`"Engineering/CI\/CD Pipeline"`).
- **Pages and databases**: title paths walk both transparently — a segment
  may be a page (children enumerated through its blocks) or a database
  (children enumerated through its rows). You never say which; the tool
  inspects each node. The tool never creates databases: below a database it
  creates rows, below a page it creates pages.
- **Parent rule**: everything up to and including a `parent` must already
  exist; everything below it is found or created. Targets given via `--page`
  or `"page"` must exist — they are resolved read-only and never created.
- **Identity is the title**: pages are matched by title below their parent on
  re-runs. An ambiguous title (two children with the same name) is an error,
  never a guess.
- **Idempotent by default**: a sha256 hash of the published content is stored
  on each page — in a `Content Hash` property for database rows, in a small
  metadata code block at the end of plain pages. Re-publishing unchanged
  content does not touch the page at all (no version churn, no edited
  timestamps, no notifications). `--force` overrides; `--append` is
  inherently non-idempotent and skips the hash logic.
- **Dry-run makes no API calls at all.**

## Single page (CLI mode)

```sh
# update an existing page, wherever it lives (never creates)
./mdnotion --token $NOTION_TOKEN --page "Engineering/Roadmap" --md roadmap.md

# create-or-update a page below a parent; title from the md's first H1 (or --title)
./mdnotion --token $NOTION_TOKEN --parent "Docs/v1.2" --md docs/installation.md

# both: --page is a path relative to --parent, found or created below it
./mdnotion --token $NOTION_TOKEN --parent "Docs/v1.2" --page "Guides/Installation" --md docs/installation.md
```

## Sets of pages (manifest mode)

A manifest is a nested JSON document: nesting expresses hierarchy, and any
string field may contain `{{param}}` placeholders filled from `--param`.

```json
{
  "parent": "{{parent}}",
  "pages": [
    {
      "title": "{{version}}",
      "children": [
        { "md": "docs/installation.md", "title": "Installation" },
        { "md": "docs/configuration.md", "title": "Configuration" }
      ]
    }
  ]
}
```

```sh
./mdnotion --token $NOTION_TOKEN --manifest docs.json \
  --param parent="Engineering Wiki/Product Docs" \
  --param version="v1.2"
```

What the parameters *mean* is entirely the caller's business — release
versions, dated snapshots, per-customer copies, or just folder names. The
tool only sees pages.

Entries can also target existing pages absolutely, anywhere in the
workspace — no shared parent needed:

```json
{
  "pages": [
    { "md": "docs/roadmap.md", "page": "Engineering/Roadmap" },
    { "md": "docs/faq.md", "page": "25301aea292980c089d7e48bbed6365a" }
  ]
}
```

Entry fields:

- `md`: markdown file providing the content. Optional for container entries
  that only hold `children`.
- `title`: the page's title and identity below its parent. Optional when
  `md` is set (falls back to the document's first `# H1`, then the file
  name). An explicit title keeps the page stable even if the H1 is reworded.
  May be a path (`"Guides/Installation"`) to create intermediate pages.
- `page`: absolute target (ID or title path) for top-level entries that
  update an existing page independent of the parent. Resolve-only.
- `children`: entries published below this entry's page.

A manifest needs a parent only if some entry is positioned relative to it;
set `"parent"` in the manifest (typically templated) or pass `--parent`.

### Cross-links between documents

Relative markdown links between manifest documents (e.g. `(./building.md)`)
are rewritten automatically to the target's Notion page URL, since those page
IDs only exist after the run creates them. Use `--rewrite-text` only for
links pointing outside the published set. Links with anchors
(`./building.md#setup`) are left untouched.

## Flags

- `--token` (required): Notion integration token
- `--page`: target page — ID or title path; relative to `--parent` when both
  are given, otherwise absolute and must exist
- `--md`: path to the markdown file (single-page mode)
- `--parent`: page below which targets are found or created; must itself exist
- `--title`: title of the page below `--parent` (default: first H1, then
  file name)
- `--manifest`: JSON manifest publishing a set of documents
- `--param name=value`: fills `{{name}}` placeholders (repeatable; works in
  manifests and in CLI references)
- `--append`: add content to the bottom instead of replacing (no hash check)
- `--force`: publish even when the content hash says nothing changed
- `--hash-property`: property name storing the hash on database rows
  (default `Content Hash`)
- `--rewrite-text mapping.json`: static link rewriting (see below)
- `--dry-run`: full conversion and plan, zero API calls
- `--debug`: verbose output
- `--version`, `-v`: print program version

### Rewrite Mapping JSON Format
- Single page mapping:
  ```json
  {
    "TEST_REPLACE": "THIS_HAS_BEEN_REPLACED"
  }
  ```
- Multi-page mapping:
  ```json
  {
    "docs/installation.md": {
      "TEST_REPLACE": "THIS_HAS_BEEN_REPLACED"
    },
    "docs/other.md": {
      "FOO": "BAR"
    }
  }
  ```

## Build Instructions

You need Go 1.24+ installed.

```sh
git clone https://github.com/antimatter-studios/mdnotion.git
cd mdnotion

go build -o mdnotion
go test ./...
```

To override the version (shown with `--version`/`-v`), build with:

```sh
go build -ldflags "-X main.Version=1.2.3" -o mdnotion
```

## Releasing with GoReleaser

This project uses [goreleaser](https://goreleaser.com/) for publishing
releases.

1. Tag a version: `git tag v1.2.3`
2. Push the tag: `git push origin v1.2.3`
3. Release: `goreleaser release --clean`

For a dry run (no publishing):

```sh
goreleaser release --clean --skip-publish --snapshot
```

See the [GoReleaser docs](https://goreleaser.com/quick-start/) for more info.
