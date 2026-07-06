# @reevit/cli

The [Reevit](https://reevit.io) command-line tool, installed via npm. The
postinstall step downloads the platform binary from
[GitHub Releases](https://github.com/Reevit-Platform/cli/releases).

```sh
npm install -g @reevit/cli
reevit login
reevit listen --forward-to http://localhost:3000/webhooks
reevit trigger payment.succeeded
```

Prefer Homebrew?

```sh
brew install reevit-platform/tap/reevit
```

Full docs live in the [CLI repository](https://github.com/Reevit-Platform/cli).
