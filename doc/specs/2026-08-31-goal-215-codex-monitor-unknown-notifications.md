# Goal 215: Codex monitor bridge の未知通知を無視する

## 背景

App Server は `remoteControl/status/changed` に文字列の `status` を含める。この通知は Codex monitor bridge の対象外だが、現状は method を判定する前に params の `status` を `codexThreadStatus` として decode するため、decode error が bridge を無効化する。

## 決定

`HandleNotification` は最初に共通で必要な `threadId` と `turn` だけを decode し、対象 thread であることを確認して method を分岐する。`status` の `codexThreadStatus` decode は `thread/status/changed` case の中だけで行う。

これにより bridge が扱わない notification は、その params schema にかかわらず error を返さず no-op になる。`remoteControl/status/changed` の文字列 `status` はこの規則の回帰例とする。

## 互換性

- `thread/status/changed` の object `status: {"type":"idle"}` は従来どおり idle と判定され、queued turn を開始する。
- `turn/started` は active を true にし、`turn/completed` は active を false にして `pumpAfterIdle` を呼ぶ。
- threadId の欠落・不一致、idle 以外の thread status、idle aliases、FIFO、retry は変更しない。
- thread status の厳密な object decode、128 MiB read limit、App Server のその他 schema、launcher の stale plugin-cache 参照は対象外とする。

## 受入条件

1. `remoteControl/status/changed` を含む未対応 method が文字列 status を持っても `HandleNotification` は error を返さない。
2. `thread/status/changed` の object idle status は既存どおり queued turn を進める。
3. `go test ./cmd/atct -count=1` が通る。
