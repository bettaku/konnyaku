# 運用と初期実装の制限

## 本番の接続構成

`PUBLIC_URL=https://translate.example.com` を公開URLに設定します。CookieのSecure属性とOrigin検証はこの値で決まります。nginx / Caddy / cloudflaredとアプリが同じホストなら、アプリは `127.0.0.1:8080` で待受します。コンテナ間接続では内部ネットワークで8080を使い、公開ポートはプロキシに限ります。

- Caddy: [Caddyfile](Caddyfile) のホスト名を書き換えます。
- nginx: [nginx.conf](nginx.conf) のホスト名・証明書パスを書き換え、httpブロック内へ読み込みます。
- Cloudflare Tunnel: remotely managed tunnelの公開ホストを `http://127.0.0.1:8080`（同一ホストのcloudflared）へルーティングします。別コンテナなら同じDockerネットワーク上のアプリのサービス名へ接続します。アプリのPUBLIC_URLは公開HTTPS URLです。[公式セットアップ](https://developers.cloudflare.com/tunnel/setup/)、[ルーティング](https://developers.cloudflare.com/tunnel/routing/)

アプリは転送ヘッダーを認証に使わず、直接接続元IPのみを扱います。Cloudflare Accessを追加する場合もアプリ自身のログインと権限は有効です。GitHub webhookにはAccessのログイン画面に遮られない経路が必要です。アプリ側の署名検証を維持してください。APIや認証済みページをCDNでキャッシュしないでください。

同期・公開処理は同期HTTP APIなので、プロキシのタイムアウトに当たる場合があります。タイムアウト後はリポジトリの状態を確認してから再操作してください。同一リポジトリで別の操作が実行中のとき、および同時実行数（3）を超えたときは待たずに429を返します。webhook経由の同期は待ち行列に残り、次のtickで再試行されます。ログインはアプリで同時実行数を制限していますが、継続的な試行のレート制御は前段で追加してください。nginx例にはログイン用の制限を含めています。

## コンテナ

ルートのDockerfileはGo 1.27.1でビルドし、CA証明書を含む非rootの実行イメージを作ります。

```sh
docker build -t konnyaku:local .
```

DB接続情報などは `.env` または実行環境のシークレット管理から渡します。既存の `docker-compose.yml` は開発DB専用です。アプリをコンテナ化するときはDBへの接続先をコンテナから到達できるホスト名に変更してください。マイグレーションは同じイメージを `migrate` 引数で実行し、成功後に `serve` で起動します。初期管理者は `ADMIN_EMAIL` / `ADMIN_NAME` / `ADMIN_PASSWORD` を渡して `create-admin` で作成します。

アプリ用の永続ボリュームは不要です。翻訳・履歴・用語集・取込文書・同期元情報・公開ジョブをPostgreSQLへ保存します。DBをバックアップし、マイグレーション前には復元可能性を確認してください。PostgreSQL 18のDBボリュームは `/var/lib/postgresql` にマウントします。[公式イメージ](https://hub.docker.com/_/postgres)

`/healthz` はプロセスの生存、`/readyz` はDB接続を確認します。

## GitHub API

プロジェクトに `https://github.com/owner/repo`（`.git` は任意）と追跡ブランチを登録します。GitHub REST APIだけで処理し、clone・ローカルファイル書き込み・Gitコマンド実行は行いません。登録・切断・プレビュー・PR作成は管理者、同期とファイル検出はプロジェクトのmanagerが操作できます。公開リポジトリはトークンなしで読み取れますが、レート制限が低いため運用では `GITHUB_TOKEN` を設定してください。非公開リポジトリの読み取りにはContents read、公開にはContents read/writeとPull requests read/writeが必要です。GitHub Appのinstallation tokenを外部で更新して渡す運用にも対応しますが、自動発行・更新は未実装です。

1. リポジトリを接続し、**Detect translation files** を実行します。Git Trees APIの一覧に既存のロケール判定を適用します。再帰一覧が打ち切られた場合はサブツリー単位で取得します。シンボリックリンクとsubmoduleは対象外です。
2. 検出結果からコンポーネントを作るか、既存コンポーネントのリポジトリ・ファイルパターンを設定します。
3. **Sync from GitHub** は開始時に追跡ブランチのcommit SHAを固定し、原文と対象言語の必要なblobだけを取り込みます。`ja` / `ja-JP`、`en_US`、Androidの `zh-rCN` とデフォルト原文に対応します。同期元のpath・blob SHA・原文SHAは文書と同じトランザクションで保存します。同じblobと原文なら取り込みを省略し、UI編集を維持します。原文だけが変わった場合は既存文書を再評価し、新しく原文に加わったキーの訳を取り込みますが、既存の訳は上書きしません。対象言語のblob自体が変わった場合は従来どおりGitHub側の値が優先されます。変更前の訳は履歴から復元できます。
4. 翻訳後、**Preview changes** で全対象言語のエクスポート差分を確認します。原文や既存の対象ファイルが同期後にGitHub側で変わった場合は停止するので、先に同期してください。プレビューはDBの一貫したスナップショットから作成して保存します。プレビュー後のUI編集を含めるには作り直してください。
5. **Open draft pull request** は保存した内容を既存treeに重ね、1つのcommitを作り、専用の `konnyaku/translations-<repository ID>-<fingerprint>` ブランチとドラフトPRを作成します。追跡ブランチへの直接書き込みやforce updateは行いません。プレビュー後に追跡ブランチが進んだ場合は新しいプレビューが必要です。公開中に追跡ブランチが進んでも、その変更を上書きせずGitHub上でPRの競合を確認できます。
6. 通信失敗後は **Review / resume previous publication** から再開します。ブランチ名、commit SHA、PR URLを保存し、GitHub側も照合して重複作成を避けます。完了後はDB内の一時的な差分本文を削除します。未着手の7日以上古いプレビューは次回のプレビュー作成時に整理します。公開開始済みの未完了ジョブは復旧用に保持します。

1ファイル4 MiB、20,000エントリまで。プレビューの変更前後の本文合計は16 MiBまでです。巨大なtreeの代替走査は1,000リクエスト・深さ64・200,000エントリで停止します。1リポジトリにつき操作は同時に1つ、1プロセスにつき最大3つです。短いレート制限・一時的なサーバーエラーでは読み取りを最大3回まで再試行し、長い待機が必要ならエラーで通知します。レート制限の待機時間を過ぎてから再試行してください。書き込みを無条件に再送することはありません。

Webhookは `POST /webhooks/github`、JSON、pushイベントを使います。`GITHUB_WEBHOOK_SECRET` と同じ32文字以上の値をGitHubへ設定してください。署名と登録URL・追跡ブランチを照合し、Delivery IDで重複排除します。ワーカーは同じリポジトリ・ブランチの処理開始時点で待機している通知をまとめ、最新の追跡ブランチをAPIで同期します。処理開始後に受信した通知は次回処理します。処理中はDB行ロックを保持し、クラッシュ時はpendingへ戻ります。競合中の操作は次回へ回します。失敗時はWebhooks画面から再試行してください。ファイル単位の成功は保存されるため、部分成功の場合があります。

### checkout方式からの移行

1. 旧バージョンの同期・公開とWebhook処理を止め、DBと既存checkoutをバックアップします。
2. 旧 `REPOSITORY_ROOT/<repository ID>` ごとに `git status`、ローカルブランチ、未pushコミットを確認します。必要な変更をGitHubへ送信するか、パッチ・Git bundleなどで退避してください。
3. 新バージョンで `konnyaku migrate` を実行し、アプリを起動します。`006_github_api.sql` が必要です。`REPOSITORY_ROOT` は使いません。旧 `/git/:action` APIは410を返します。
4. 各リポジトリでAPI同期を実行し、翻訳・進捗・プレビューを確認します。初回同期にはAPI方式の基準データがないため、GitHub側の訳を取り込みます。DBだけにある未公開の訳は事前にエクスポートして保護し、必要に応じて同期後に戻してください。
5. ローカルだけにある変更の回収とAPI経由の公開を確認後、管理者が旧checkoutと不要なボリュームを削除します。アプリは既存checkoutを自動削除しません。

## 機械翻訳

OpenAI互換プロバイダーでは `OPENAI_BASE_URL`（`/v1`まで）、`OPENAI_API_KEY`、`OPENAI_MODEL` を設定します。モデルは利用環境に合わせて明示してください。ローカル互換サーバー向けにHTTPも許可しています。[Chat Completions](https://developers.openai.com/api/reference/cli/resources/chat/subresources/completions)

Googleは `GOOGLE_CLOUD_PROJECT`、`GOOGLE_CLOUD_LOCATION`（既定値global）とApplication Default Credentialsを用います。API有効化と翻訳権限が必要です。OAuth2クライアントがトークンを更新します。v3の `projects/.../locations/...:translateText` へ `mimeType: text/plain` で送信します。[公式説明](https://docs.cloud.google.com/translate/docs/translate-text)

ボタンを押すと原文が選択したプロバイダーへ送信され、料金が発生する場合があります。候補は自動保存されません。内容とプレースホルダーを確認して保存してください。

## 残作業・既知の制限

- PO複数形、Android plurals/string-array/styled text、配列や文字列以外を含むJSON/YAMLは拒否します。
- ファイルは4 MiB / 20,000エントリまで。JSONは再整形され、PO改行はLFに正規化されます。完全なバイト一致は保証しません。
- 取込はファイル単位のトランザクションで処理します。手動取込とGitHub側で変更された対象言語ファイルの取込はUI編集を上書きします。原文から消えたキーは自動削除しません。
- 対象言語の取込文書をエクスポートのテンプレートに使うため、後から原文に追加されたキーとの完全な同期は未実装です。
- ユーザー無効化・パスワード変更/リセット・招待・SSO・監査ログは未実装です。
- 同期・公開は同期HTTP APIです。永続化した公開ジョブの手動再開には対応しますが、自動バックグラウンド公開・競合解決UI・GitHub Appトークンの自動更新は未実装です。
- 進捗率は「translated または reviewed の件数 ÷ 原文の件数」です。原文と同じ文字列がコピーされている訳も翻訳済みとして数えます。needs review は棒グラフに別色で表示され、率には含まれません。
- 機械翻訳の予算制御・再試行・プレースホルダー検査は未実装です。用語集は原文中の一致を示し訳語の有無を表示するだけで、保存を止めません。翻訳メモリは閲覧権限のあるプロジェクト間で `pg_trgm` の類似度により候補を出します（`003` マイグレーションが `CREATE EXTENSION pg_trgm` を実行するため、DBユーザーにその権限が必要です）。
- 複数台運用、本番サービス・プロキシでの検証は残作業です。

認証・権限・競合・形式変換のテストは開発用です。本番公開前に、この制限を運用要件と照合してください。
