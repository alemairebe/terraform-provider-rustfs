The **Prepare Release** workflow now opens or updates the release pull request itself, using a
short-lived GitHub App installation token instead of `GITHUB_TOKEN` so the `pull_request` checks
run on it. The job summary links the pull request; when the `APP_ID` variable and
`APP_PRIVATE_KEY` secret are absent (forks, or before the App exists) the branch is still pushed
and the summary prints the manual compare link.
