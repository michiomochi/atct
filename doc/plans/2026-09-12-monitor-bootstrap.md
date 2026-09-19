# Monitor Bootstrap Implementation Plan

**Goal:** 起動引数ではなく canonical session から導出した assignment で Claude Watch と Codex Bridge を bind する。

**Architecture:** monitor token と SessionStart key の対応だけを store に永続化する。HTTP API は token から現在の assignment を返し、各 monitor は bind 更新時に既存の scope watch を再作成する。role の導出は daemon と HTTP API で同じ store 実装を使う。

## Task 1: Store に assignment と monitor binding を追加する

- [ ] assignment（commander の project、subcommander の goal、executor の全 open task handoff）を返す store API を追加する。
- [ ] token と session key を結ぶ migration / store API を追加する。
- [ ] task が複数ある executor の assignment を含む store test を RED → GREEN で追加する。

## Task 2: daemon / HTTP API の bind 契約を追加する

- [ ] `session.identify` が SessionStart 済み token を canonical session に結び、assignment を返すようにする。
- [ ] token から pending / assignment を取得する HTTP endpoint を追加する。
- [ ] identify、claim、handoff receive、complete 後に同じ assignment を返す API test を追加する。

## Task 3: Codex monitor を assignment bind 待ちへ移行する

- [ ] wrapper が token を生成し、Codex の SessionStart hook に注入する。
- [ ] SessionStart hook が token と session key を登録する。
- [ ] monitor は bind 前に watch せず、binding が変わるたびに scope watch を再作成する。
- [ ] `--role` / selector を廃止し、generic command の lifecycle test を RED → GREEN で追加する。

## Task 4: Claude Watch と文書・スキルを同じ契約へ寄せる

- [ ] Watch の scope 指定を assignment lookup に置き換える。
- [ ] `doc/continuous-execution.md`、`doc/bootstrap.md`、ATCT skill / start skill の古い起動形式を更新する。
- [ ] unit / integration tests と `go test ./... -count=1` を実行する。
