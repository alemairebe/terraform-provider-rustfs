Releasing no longer edits shared files by hand. The new **Prepare Release** workflow bundles every
`changelog.d/` fragment into a dated `CHANGELOG.md` section, regenerates the resource and data source
tables in `AGENTS.md` and `README.md`, and pushes the result to a `release/v<version>` branch. It
refuses to run when the branch would contain anything else, and the version is resolved by
`scripts/next-version.sh`, which **Create Release Tag** now shares so the two workflows cannot
disagree. Opening the pull request and creating the tag stay manual.
