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

Git 連携はプロジェクト単位の「リポジトリ」で設定します。URLは `https://github.com/owner/repo`（`.git` の有無は問いません）、追跡ブランチは既定で `main` です。現状はGitHub HTTPSのみで、SSH・任意ホスト・submoduleは未対応です。リポジトリの接続・clone/pull/push・PR作成には管理者権限、checkoutからの同期（Sync）と翻訳ファイル検出にはプロジェクトのmanager権限が必要です。checkoutは `REPOSITORY_ROOT/<リポジトリID>` に置かれます。パストラバーサルと翻訳パスのシンボリックリンクを拒否し、Gitフックとグローバル設定を無効化しています。

`GITHUB_TOKEN` には対象リポジトリで必要なContents / Pull requests権限を付与してください。UIでの手順は次のとおりです。

1. プロジェクト画面でリポジトリを接続し、リポジトリ画面で **Clone** します。
2. **Detect translation files** で checkout 内の翻訳ファイルを検出します。`dir/{locale}.ext`、`dir/{locale}/file.ext`、`values-{locale}/strings.xml` の配置を認識し、候補からコンポーネントを作成できます。未登録のロケールは先にロケール管理で追加してください。既存コンポーネントは設定画面でリポジトリとファイルパターンを紐付けられます。
3. **Sync from checkout** で、リポジトリに紐付く全コンポーネントについて、原文ファイルと checkout に存在する全ロケールファイルを取り込みます。検出結果からコンポーネントを作成した場合は自動で実行されます。ファイル名の `ja` と登録ロケール `ja-JP` のような差、`en_US` 形式、Android の `values-zh-rCN` / 原文の `values/strings.xml` は自動で対応付けられ、リポジトリにあるロケールは未登録でも自動登録されます。空の値（PO の空 msgstr など）は未翻訳として扱われ、原文に無いキーはスキップして件数を報告し（ファイル全体は拒否しません）、コンポーネント画面に「原文に無いキー」として値付きで一覧されます。原文にキーが追加されると自動で消え、manager は個別・ロケール単位で dismiss できます。同期はファイル単位で継続し、結果はリポジトリ画面の「Sync report」に取込件数・未知キー数・無視したファイル・エラーとして表示されます。1 ファイル 20,000 エントリまでで、数千エントリのファイルが数十個あるリポジトリでは同期に数十秒かかります。
4. 翻訳後、**Open draft pull request** を押すと、pull → `konnyaku/translations-<UTC時刻>` ブランチ作成 → 全対象ロケールをエクスポート → commit → push → 追跡ブランチ向けのドラフトPR作成を行い、checkoutは追跡ブランチへ戻ります。変更が無い場合はエラーで止まります。追跡ブランチへ直接 commit / push する操作も用意しています。[GitHub PR API](https://docs.github.com/en/rest/pulls/pulls#create-a-pull-request)

WebhookのPayload URLは `https://公開ホスト/webhooks/github`、Content typeはJSON、イベントはpushです。`GITHUB_WEBHOOK_SECRET` と同じ32文字以上のランダム値をGitHubへ設定します。署名を検証し、登録済みリポジトリURL・追跡ブランチに一致したイベントだけをDBキューへ保存します。Delivery IDで重複を排除します。[署名検証](https://docs.github.com/en/webhooks/using-webhooks/validating-webhook-deliveries)

ワーカーはclone/pull後に、そのリポジトリに紐付く全コンポーネントでSyncと同じ取り込みを行います。対象ロケールのファイルも取り込まれるため、UIで編集中の翻訳がリポジトリ側の値で上書きされることがあります。翻訳の変更はすべて履歴に残るので、上書きされた値は履歴パネルから確認できます。失敗時は管理画面（Webhooks）で状態を確認し、競合や接続設定を直して再試行します。ジョブ処理中はDB行ロックを保持し、クラッシュ時のロールバックでpending状態へ戻ります。複数コンポーネントを含むイベントは途中まで成功する場合があります。

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
- Git操作は同期APIです。非同期ジョブ化・競合解決UI・GitHub App認証は未実装です。
- 進捗率は「translated または reviewed の件数 ÷ 原文の件数」です。原文と同じ文字列がコピーされている訳も翻訳済みとして数えます。needs review は棒グラフに別色で表示され、率には含まれません。
- 機械翻訳の予算制御・再試行・プレースホルダー検査は未実装です。用語集は原文中の一致を示し訳語の有無を表示するだけで、保存を止めません。翻訳メモリは閲覧権限のあるプロジェクト間で `pg_trgm` の類似度により候補を出します（`003` マイグレーションが `CREATE EXTENSION pg_trgm` を実行するため、DBユーザーにその権限が必要です）。
- 複数台運用、本番サービス・プロキシでの検証は残作業です。

認証・権限・競合・形式変換のテストは開発用です。本番公開前に、この制限を運用要件と照合してください。
