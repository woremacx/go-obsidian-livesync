# CouchDB の `_rev` (revision) の仕組み

## 基本概念

CouchDB のすべてのドキュメントには `_id` と `_rev` がある。

```json
{
  "_id": "f:abc123",
  "_rev": "3-a5b8e9f2c1d4e7b0",
  "type": "newnote",
  "path": "/\\:encrypted...",
  "children": ["h:+xxx", "h:+yyy"]
}
```

`_rev` のフォーマットは `{世代番号}-{ハッシュ}` (例: `3-a5b8e9f2c1d4e7b0`)。

## なぜ `_rev` が必要か — 楽観的ロック

CouchDB はロックを取らない。代わりに **楽観的並行性制御 (Optimistic Concurrency Control)** を使う。

### ルール: ドキュメントを更新するには、現在の `_rev` を知っていなければならない

```
# 初回作成 — _rev 不要
PUT /db/doc1  {"data": "hello"}
→ 201 Created  {"rev": "1-abc"}

# 更新 — 正しい _rev を指定しないと拒否される
PUT /db/doc1  {"_rev": "1-abc", "data": "world"}
→ 201 Created  {"rev": "2-def"}

# 古い _rev で更新しようとすると…
PUT /db/doc1  {"_rev": "1-abc", "data": "conflict!"}
→ 409 Conflict
```

これは Git のようなもの。push するには最新の commit (rev) の上に積む必要がある。古い commit の上に積もうとすると reject される。

## 具体的なシナリオで理解する

### シナリオ1: 単純な更新（問題なし）

```
時刻1: CouchDB上のドキュメント
  _id: "f:abc"  _rev: "1-aaa"  内容: "Hello"

時刻2: Go CLI が更新したい
  1. GET /db/f:abc → _rev: "1-aaa" を取得
  2. PUT /db/f:abc  {"_rev": "1-aaa", "data": "World"}
  → 成功。新しい _rev: "2-bbb" が発行される
```

### シナリオ2: 競合（問題あり）

```
時刻1: CouchDB上のドキュメント
  _id: "f:abc"  _rev: "1-aaa"  内容: "Hello"

時刻2: Go CLI が _rev を取得
  GET /db/f:abc → _rev: "1-aaa"

時刻3: その間に JS plugin が同じドキュメントを更新
  PUT {"_rev": "1-aaa", "data": "From JS"}
  → 成功。_rev は "2-ccc" になった

時刻4: Go CLI が古い _rev で更新しようとする
  PUT {"_rev": "1-aaa", "data": "From Go"}
  → 409 Conflict!  (もう "1-aaa" は最新じゃない)
```

Go CLI は `1-aaa` を基に更新しようとしたが、すでに JS plugin が `1-aaa` → `2-ccc` に進めていた。CouchDB は「お前の知っている状態はもう古い」と拒否する。

## PouchDB (JS版) はどう解決するか

PouchDB のレプリケーションでは **409 が起きない**。なぜなら：

1. PouchDB は `_bulk_docs` に `new_edits: false` を付けて送る
2. これは「revision tree をそのまま受け入れろ」という意味
3. CouchDB は両方の revision を保持して **conflict マーカー** を付ける
4. アプリ側が後で conflict を解決する（LiveSync には conflict resolver がある）

```
CouchDB の revision tree:

        1-aaa
       /     \
    2-ccc   2-ddd    ← 両方保持。片方が "winning rev"、もう片方が conflict
  (JS版)   (PouchDB)
```

## Go CLI の現状の問題

`cmd/internal/push/push.go` と `cmd/internal/couchdb/client.go` では：

1. `GetDocRev(id)` で現在の `_rev` を取得
2. `PutDoc(id, doc)` で `_rev` を指定して PUT

これは **通常の HTTP API** を使っている。`new_edits: false` ではない。つまり：

- 初回 push (ドキュメントが存在しない) → 問題なし
- 既存ドキュメントの更新 → `_rev` を取得してから PUT するまでの間に誰かが更新すると **409 Conflict** になる
- そのエラーハンドリングが現状ない

## どうすべきか — 選択肢は3つ

### A. 現状のまま（楽観的ロック）

- `GetDocRev` → `PUT` with `_rev` → 409 なら再取得してリトライ
- シンプルだが、JS plugin が continuous sync している環境では競合しやすい

### B. `new_edits: false` で BulkDocs（PouchDB方式）

- CouchDB が revision tree に追加するだけなので 409 が起きない
- ただし自分で正しい revision tree を構築する必要がある（世代番号の管理が複雑）

### C. Pull first で競合を減らす（現状の main.go の方針）

- Push 前に pull して最新状態にする → その上で push
- 競合の確率は下がるが、ゼロにはならない

## 現状

現状の Go CLI は **C + A のリトライなし版**。実用上は pull first でほぼ問題ないが、リトライは足すべき。
