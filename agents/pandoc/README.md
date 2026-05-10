# pandoc

Generic pandoc agent. Convert between any pandoc-supported pair —
markdown, html, docx, pdf, epub, latex, rst, asciidoc, plain.

## Build & run

```sh
otters build agents/pandoc -t pandoc:latest
otters run pandoc:latest --name pandoc -v $PWD/docs:/docs
otters chat pandoc
```

## Try it

```
> /docs/post.md -> /docs/post.html
> /docs/notes.md -> docx with a TOC
> /docs/article.md -> pdf with author "Me" and date "today"
> convert every .md under /docs to epub
> /docs/page.html -> markdown
```

PDF output needs a LaTeX engine in the image. If pdflatex is
missing, the agent falls back to suggesting `--pdf-engine=wkhtmltopdf`
or HTML output.

## Tools

- `pandoc` (vendored) — primary
- `ls` — confirm inputs / outputs
- `sh` — batch loops over many files
