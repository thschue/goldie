# Writing content

## Your first article

An article is one Markdown file in `global/articles/` under your content directory. The file name,
without `.md`, is the article's ID: `global/articles/deploy.md` is the article `deploy`. Folders under
`global/articles/` become part of the ID (`sub/deploy`).

This is the smallest file that loads:

```markdown
---
name: Deploying a service
keywords: [deploy, release]
---
Run the pipeline from main. Tag the release first.
```

With the optional `updated` key:

```markdown
---
name: Deploying a service
keywords: [deploy, release]
updated: 2024-03-09
---
Run the pipeline from main. Tag the release first.
```

- `name` is the title. It is required and may not be blank.
- `keywords` is a list of words a search matches, besides the words in `name`. Add the words people
  will type.
- `updated` is optional: the day you last changed the article, as `YYYY-MM-DD`. Goldie shows it as the
  change time and never sets it for you, so bump it when you edit. Any other value fails the load.
- Everything after the second `---` is the body, in Markdown.

Check it and find it. No server is needed for this check.

```sh
goldie validate .
goldie search deploy --dir .
```

`goldie validate` exits 0 and prints nothing when the content loads. `goldie search` prints the
article. A file that starts with anything other than `---` fails the load, and the error shows this
frontmatter block. More fields (`status`, `enforced`, `replaced_by`) and the rest of the rules are in
[Checking content in CI](content-ci.md).
