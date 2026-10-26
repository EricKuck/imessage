# mautrix-imessage

A Matrix-iMessage puppeting bridge built on [mautrix-go](https://github.com/mautrix/go)'s
bridgev2 framework. It talks to a [BlueBubbles](https://bluebubbles.app) server over its
REST API and websocket, so the bridge itself can run anywhere; only BlueBubbles needs a Mac.

This is a rewrite of the legacy mautrix/imessage bridge that keeps only the BlueBubbles
connector. The macOS, iOS and Android SMS connectors were dropped.

## Features

- Text, attachments, replies, tapbacks, edits, unsends, read receipts and typing
  notifications in both directions (most of these need the BlueBubbles private API)
- Group chats with names, icons and membership
- History backfill
- End-to-bridge encryption (including MSC4190 for Matrix Authentication Service setups)
- Double puppeting with an `as_token:` secret
- Starting new DMs with a phone number or email, and contact list lookups

## Setup

Build with `go build -tags goolm ./cmd/mautrix-imessage` (or without the tag to link libolm),
then follow the [standard mautrix bridge setup](https://docs.mau.fi/bridges/general/).
Once the bridge is running, send `login` to the bridge bot and give it the BlueBubbles server
URL and password. Install `ffmpeg` next to the bridge to send voice messages.
