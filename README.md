# go-obsidian-livesync

[Obsidian LiveSync](https://github.com/vrtmrz/obsidian-livesync) の CouchDB からVaultを復元・同期するための Go 実装 CLI ツールです。

オリジナルの LiveSync プラグインは [vrtmrz/obsidian-livesync](https://github.com/vrtmrz/obsidian-livesync) で開発されています。本リポジトリは、そのデータフォーマットと暗号化方式に互換性のある独立した CLI 実装です。

## コマンド

### livesync-pull

CouchDB からドキュメントをローカル SQLite にレプリケーションし、Vault ディレクトリにファイルを復元します。

```sh
LIVESYNC_PASSPHRASE="your-passphrase" \
  go run ./cmd/livesync-pull \
    --url https://couchdb.example.com \
    --db obsidian \
    --user admin \
    --pass secret \
    --vault ./vault
```

| フラグ | 説明 |
|---|---|
| `--url` | CouchDB URL |
| `--db` | データベース名 |
| `--user` | CouchDB ユーザー名 |
| `--pass` | CouchDB パスワード |
| `--vault` | 出力 Vault ディレクトリ (デフォルト: `./vault`) |
| `--data` | SQLite ファイルパス (デフォルト: `.<db>.db`) |
| `--full` | インクリメンタル検出をスキップして全ファイルを再構築 |
| `--watch` | CouchDB の変更を longpoll で監視し、継続的に Vault を更新 |
| `--dynamic-iter` | V1 暗号化の動的イテレーションカウントを使用 |
| `-v` | ログ詳細度: `debug` または `trace` |

### livesync-push

ローカル Vault ディレクトリの変更を検出し、CouchDB にプッシュします。

```sh
LIVESYNC_PASSPHRASE="your-passphrase" \
  go run ./cmd/livesync-push \
    --url https://couchdb.example.com \
    --db obsidian \
    --user admin \
    --pass secret \
    --vault ./vault
```

| フラグ | 説明 |
|---|---|
| `--url` | CouchDB URL |
| `--db` | データベース名 |
| `--user` | CouchDB ユーザー名 |
| `--pass` | CouchDB パスワード |
| `--vault` | Vault ディレクトリ (デフォルト: `./vault`) |
| `--data` | SQLite ファイルパス (デフォルト: `.<db>.db`) |
| `--force` | 全ファイルのコンテンツハッシュを比較 |
| `--dry-run` | 変更検出のみ (プッシュしない) |
| `-v` | ログ詳細度: `debug` または `trace` |

## ビルド

```sh
go build -o livesync-pull ./cmd/livesync-pull
go build -o livesync-push ./cmd/livesync-push
```

## テスト

```sh
go test ./...
```

## 対応する暗号化方式

- HKDF (現行方式)
- V1-Hex / V1-JSON (レガシー)
- V3 (レガシー)

## ライセンス

```
Copyright (c) 2021 vorotamoroz
Copyright (c) 2026 woremacx

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```
