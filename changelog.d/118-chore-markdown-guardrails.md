The Markdown conventions are now enforced on every pull request instead of being a written rule.
CI fails when a change edits `CHANGELOG.md` outside a release branch, omits a
`changelog.d/<issue>-<kind>-<slug>.md` fragment (unless the `skip-changelog` label is set), or
touches `AGENTS.md`, `README.md`, `docs/index.md` or `templates/` without the `shared-md` label.
The generated tables are checked for deterministic output rather than for being current, so a
feature PR still adds only its own files. `docs/` drift is now reported on the pull request that
causes it rather than on `main` after the merge.
