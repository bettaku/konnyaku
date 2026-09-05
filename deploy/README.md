# 運用と初期実装の制限

## 本番の接続構成

`PUBLIC_URL=https://translate.example.com` を公開URLに設定します。CookieのSecure属性とOrigin検証はこの値で決まります。nginx / Caddy / cloudflaredとアプリが同じホストなら、アプリは `127.0.0.1:8080` で待受します。コンテナ間接続では内部ネットワークで8080を使い、公開ポートはプロキシに限ります。

- Caddy: [Caddyfile](Caddyfile) のホスト名を書き換えます。
- nginx: [nginx.conf](nginx.conf) のホスト名・証明書パスを書き換え、httpブロック内へ読み込みます。
- Cloudflare Tunnel: remotely managed tunnelの公開ホストを `http://127.0.0.1:8080`（同一ホストのcloudflared）へルーティングします。別コンテナなら同じDockerネットワーク上のアプリのサービス名へ接続します。アプリのPUBLIC_URLは公開HTTPS URLです。[公式セットアップ](https://developers.cloudflare.com/tunnel/setup/)、[ルーティング](https://developers.cloudflare.com/tunnel/routing/)

アプリは転送ヘッダーを認証に使わず、直接接続元IPのみを扱います。Cloudflare Accessを追加する場合もアプリ自身のログインと権限は有効です。GitHub webhookにはAccessのログイン画面に遮られない経路が必要です。アプリ側の署名検証を維持してください。APIや認証済みページをCDNでキャッシュしないでください。

Git操作は同期APIなので、プロキシのタイムアウトに当たる場合があります。タイムアウト後はリポジトリの状態を確認してから再操作してください。ログインはアプリで同時実行数を制限していますが、継続的な試行のレート制御は前段で追加してください。nginx例にはログイン用の制限を含めています。

## コンテナ

ルートのDockerfileはGo 1.27.1でビルドし、GitとCA証明書を含む非rootの実行イメージを作ります。

```sh
docker build -t konnyaku:local .
```

DB接続情報などは `.env` または実行環境のシークレット管理から渡します。既存の `docker-compose.yml` は開発DB専用です。アプリをコンテナ化するときはDBへの接続先をコンテナから到達できるホスト名に変更してください。マイグレーションは同じイメージを `migrate` 引数で実行し、成功後に `serve` で起動します。初期管理者は `ADMIN_EMAIL` / `ADMIN_NAME` / `ADMIN_PASSWORD` を渡して `create-admin` で作成します。

リポジトリ用の `/data` をUID 10001が書ける永続ボリュームへマウントしてください。DBとリポジトリをバックアップし、マイグレーション前には復元可能性を確認します。PostgreSQL 18のDBボリュームは `/var/lib/postgresql` にマウントします。[公式イメージ](https://hub.docker.com/_/postgres)

初期バージョンはアプリ1台とローカルの永続ボリュームを前提にします。`/healthz` はプロセスの生存、`/readyz` はDB接続を確認します。

## Git / GitHub

URLは `https://github.com/owner/repo.git`、パターンは `locales/{locale}.json` の形式です。現状はGitHub HTTPSのみで、SSH・任意ホスト・submoduleは未対応です。Git操作・接続設定・PR作成には管理者権限が必要です。パストラバーサルと翻訳パスのシンボリックリンクを拒否し、Gitフックとグローバル設定を無効化しています。

`GITHUB_TOKEN` には対象リポジトリで必要なContents / Pull requests権限を付与してください。UIでClone → Gitからインポート → 翻訳 → Commit → Pushの順に操作します。PRを作るにはコンポーネントを既存の翻訳専用ブランチに設定し、異なるマージ先ブランチを指定します。APIはドラフトPRを作ります。[GitHub PR API](https://docs.github.com/en/rest/pulls/pulls#create-a-pull-request)

WebhookのPayload URLは `https://公開ホスト/webhooks/github`、Content typeはJSON、イベントはpushです。`GITHUB_WEBHOOK_SECRET` と同じ32文字以上のランダム値をGitHubへ設定します。署名を検証し、登録済みURL・ブランチに一致したイベントだけをDBキューへ保存します。Delivery IDで重複を排除します。[署名検証](https://docs.github.com/en/webhooks/using-webhooks/validating-webhook-deliveries)

ワーカーはclone/pull後に原文だけを取り込み、UI編集中の対象言語は変更しません。失敗時は管理画面で状態を確認し、競合や接続設定を直して再試行します。ジョブ処理中はDB行ロックを保持し、クラッシュ時のロールバックでpending状態へ戻ります。複数コンポーネントを含むイベントは途中まで成功する場合があります。

## 機械翻訳

OpenAI互換プロバイダーでは `OPENAI_BASE_URL`（`/v1`まで）、`OPENAI_API_KEY`、`OPENAI_MODEL` を設定します。モデルは利用環境に合わせて明示してください。ローカル互換サーバー向けにHTTPも許可しています。[Chat Completions](https://developers.openai.com/api/reference/cli/resources/chat/subresources/completions)

Googleは `GOOGLE_CLOUD_PROJECT`、`GOOGLE_CLOUD_LOCATION`（既定値global）とApplication Default Credentialsを用います。API有効化と翻訳権限が必要です。OAuth2クライアントがトークンを更新します。v3の `projects/.../locations/...:translateText` へ `mimeType: text/plain` で送信します。[公式説明](https://docs.cloud.google.com/translate/docs/translate-text)

ボタンを押すと原文が選択したプロバイダーへ送信され、料金が発生する場合があります。候補は自動保存されません。内容とプレースホルダーを確認して保存してください。

## 残作業・既知の制限

- PO複数形、Android plurals/string-array/styled text、配列や文字列以外を含むJSON/YAMLは拒否します。
- ファイルは4 MiB / 10,000エントリまで。JSONは再整形され、PO改行はLFに正規化されます。完全なバイト一致は保証しません。
- 取込は全体をトランザクションで処理しますが、対象言語の再取込はUI編集を上書きします。原文から消えたキーは自動削除しません。
- 対象言語の取込文書をエクスポートのテンプレートに使うため、後から原文に追加されたキーとの完全な同期は未実装です。
- ユーザー無効化・パスワード変更/リセット・招待・SSO・監査ログは未実装です。
- Gitのブランチ作成・非同期ジョブ化・競合解決UI・GitHub App認証は未実装です。
- 機械翻訳の予算制御・再試行・用語集・翻訳メモリ・プレースホルダー検査は未実装です。
- 大規模カタログ向けDB検索・進捗集計、複数台運用、本番サービス・プロキシでの検証は残作業です。

認証・権限・競合・形式変換のテストは開発用です。本番公開前に、この制限を運用要件と照合してください。
