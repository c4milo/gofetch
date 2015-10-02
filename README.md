# Gofetch
Go library to download files from the internerds.

## Features
* Resumes downloads if interrupted
* Allows parallel downloading of a single file by requesting multiple data chunks at once
* Reports download progress through a Go channel if indicated to do so
* Can be combined with https://github.com/cenkalti/backoff to support retries with
exponential back-off
