<p align="center">
  <img src="https://raw.githubusercontent.com/badmintoncryer/cfn-exec-policy/main/assets/logo.png" alt="cfn-exec-policy" width="104" height="104">
</p>

<h1 align="center">cfn-exec-policy</h1>

<p align="center">
  <strong>AWS CDK の CloudFormation 実行ロールを、必要最小限の権限に。</strong>
</p>

<p align="center">
  <a href="https://github.com/badmintoncryer/cfn-exec-policy/actions/workflows/ci.yml"><img src="https://github.com/badmintoncryer/cfn-exec-policy/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://www.npmjs.com/package/cfn-exec-policy"><img src="https://img.shields.io/npm/v/cfn-exec-policy.svg" alt="npm version"></a>
  <a href="https://www.npmjs.com/package/cfn-exec-policy"><img src="https://img.shields.io/npm/dt/cfn-exec-policy.svg" alt="npm total downloads"></a>
</p>

<p align="center"><a href="https://github.com/badmintoncryer/cfn-exec-policy/blob/main/README.md">English</a> | <b>日本語</b></p>

**AWS CDKとCloudFormationのためのツールです**。CloudFormationの実行ロールに本当に必要なIAMポリシーを、テンプレートから作ります。`cdk bootstrap`が実行ロールに`AdministratorAccess`を付けるのをやめさせられます。

```sh
cdk synth
npx cfn-exec-policy apply        # 管理ポリシー "cfn-exec-policy" を作成または更新
# → 表示: cdk bootstrap aws://123456789012/us-east-1 --cloudformation-execution-policies arn:aws:iam::123456789012:policy/cfn-exec-policy
```

表示された`cdk bootstrap`のコマンドを1回実行すれば、その環境のデプロイはすべて、管理者権限ではなく生成したポリシーで行われます。アプリが独自のbootstrap qualifierを使っている場合、表示するコマンドには、そのbootstrapの`--qualifier`と`--toolkit-stack-name`も付きます（`cdk.out`から読み取ります）。そのため、既定の`CDKToolkit`スタックを書き換えることはありません。

テンプレートを変えたら、デプロイの前にポリシーを更新します。

```sh
cdk synth
npx cfn-exec-policy apply        # 新しいテンプレートに要る権限を足す
cdk deploy
```

CIでは`npx cfn-exec-policy check`を使います。ポリシーに足りない権限があるとジョブが失敗するので、デプロイの途中ではなく、始める前に気づけます。`cdk bootstrap`をやり直すのは、`apply`が表示するポリシーのARNの一覧が変わったとき（ポリシーが大きくなって2つ以上に分かれたとき）だけです。

## コマンド

| コマンド | すること | AWSの認証情報 |
|---|---|---|
| `generate [入力]` | ポリシーのJSONを表示する。`--format terraform`で`aws_iam_policy`リソース（パートごとに1つ）を、`--format cdk`で`ManagedPolicy`を作るTypeScriptの関数を出力する。`apply`を使わず、IAMをコードで管理するチーム向け。`--explain`を付けると、代わりにアクションごとに、それを必要とするリソースと理由（スキーマのハンドラー、手で測った行、`<サービス>:*`での補完、カスタムリソース、ネストスタック、SSMパラメータ型、動的参照）を表示する。`--format json`でJSONにもできる | 不要 |
| `check [入力]` | テンプレートに要る権限が管理ポリシーに足りなければ、終了コード1で終わる。`cdk deploy`の前にCIで実行する。ポリシーは名前で読むだけで、実行ロールに付いているかまでは確かめない | 要（読み取りのみ） |
| `diff --base <パス> [入力]` | 別の`cdk.out`（またはテンプレート）、たとえば`main`からsynthしたものと比べて、変更で増える権限と要らなくなる権限を表示する。[プルリクエストで使う](#プルリクエストで使う)を参照 | 不要 |
| `apply [入力]` | 管理ポリシーを作成または更新し、`cdk bootstrap`のコマンドを表示する | 要 |
| `delete` | 管理ポリシーを、分割した分と`--strict`のboundaryも含めて削除する。どれかがまだロールに付いていれば止まる。`--yes`を付けなければ、削除するポリシーを表示するだけ | 要 |
| `doctor <stack>` | スタックの直近のデプロイで出た権限エラーを説明する。[デプロイが失敗したとき](#デプロイが失敗したとき)を参照 | 要（読み取りのみ） |

入力には、`cdk.out`のディレクトリか、CloudFormationのテンプレート（JSONまたはYAML）を渡します。省略すると`./cdk.out`を読みます。ネストスタックとStageも含めて読みます。

フラグ: `--policy-name`（既定は`cfn-exec-policy`）、`--refresh-schemas`（同梱の表ではなく、最新のCloudFormationスキーマを使う）、`--pass-role-condition`（`iam:PassRole`を渡し先のサービスで絞る。後述）、`--region-condition`（スタックのリージョンに絞る。後述）、`--strict`と`--boundary`（[`--strict`](#--strict)を参照）、`--account`（[複数のアカウント](#複数のアカウント)を参照）、`--scope-resources`（[リソースを絞る](#リソースを絞る)を参照）、`apply --prune`、`apply --drop-unused`（[使われていないサービスを外す](#使われていないサービスを外す)を参照）、`delete --yes`、`diff --format`と`diff --fail-on`（後述）、`check --validate`と`check --no-new-access`（[IAM Access Analyzer](#iam-access-analyzer)を参照）。

### デプロイが失敗したとき

実行ロールの権限が足りずにデプロイが失敗したら、`doctor`がスタックの直近の操作（と、その中で失敗したネストスタック）を読み、拒否されたリソースごとに、エラーに書かれたアクションを並べます。

```sh
npx cfn-exec-policy doctor MyStack --region eu-west-1
```

ポリシーがすでに許しているアクションは、ほかのもの（permissions boundary、SCP、リソースポリシー、条件）に拒否されたと表示します。アクションが書かれていないエラー（S3の`Access Denied`など）は、CloudTrailを見るよう案内します。権限の表に無いアクションは、issueに貼る行を出します。`--format json`で同じ内容をJSONで出力します。権限エラーが見つかると終了コード`3`で終わります。

### 使われていないサービスを外す

同じアカウントとリージョンでブートストラップしたアプリはすべて同じポリシーを使うので、`apply`はアクションを消しません。`apply --drop-unused 90`は、IAMの[最終アクセス情報](https://docs.aws.amazon.com/ja_jp/IAM/latest/UserGuide/access_policies_last-accessed.html)から、ポリシーが付いたロールが90日間呼んでいないサービスのアクションを外します。テンプレートに要るものは外しません。ポリシーが作られてからその日数が経っていなければ止まります（まだ使っていないだけかもしれないため）。`check --no-new-access`と同じく、ブートストラップを共有する全アプリのcdk.outを渡してください。

### リソースを絞る

既定では、どのステートメントも`Resource: "*"`です。`--scope-resources`（試験的。実アカウントではまだ確かめていません）は、SQSとSNSのアクションを、スタック自身のキューとトピックに絞ります。CloudFormationがスタック名から付ける名前（`arn:*:sqs:*:*:MyStack-*`）、テンプレートで固定した名前、テンプレートに書かれたキューURLやトピックARNが対象です。テンプレートから解決できないキューやトピックを指すリソース（インポートしたトピック、パラメータから来る名前）や、まだ絞れない型があれば、そのサービスは警告を出して`*`のままにします。入力は`cdk.out`ディレクトリである必要があります。`--prune`なしの`apply`は、ポリシーにすでにあるキューとトピックを残し、ポリシーがまだ`*`で許しているサービスは`*`のままにします。対象の型は[#32](https://github.com/badmintoncryer/cfn-exec-policy/issues/32)で増やしていきます。

### IAM Access Analyzer

- `check --validate`は、テンプレートに要るポリシー（`--strict`なら、`apply`が書くboundaryも）をAccess Analyzerの[`ValidatePolicy`](https://docs.aws.amazon.com/access-analyzer/latest/APIReference/API_ValidatePolicy.html)にかけ、見つかったものをすべて表示します。エラーとセキュリティ警告があれば失敗します。
- `check --no-new-access`は、Access Analyzerの[`CheckNoNewAccess`](https://docs.aws.amazon.com/access-analyzer/latest/APIReference/API_CheckNoNewAccess.html)で、デプロイ済みのポリシーの各パートが、テンプレートに要る以上の権限を与えていないかを確かめます。広げられた`Resource`や外された`Condition`など、通常の`check`では見えない差も対象です。与えていれば失敗します。複数のアプリでブートストラップを共有している場合は、ほかのアプリのアクションもここに出るので、全アプリの入力を渡して実行してください。

どちらもAPIの呼び出しが増えます。Access Analyzerのカスタムポリシーチェックは[呼び出しごとに課金](https://aws.amazon.com/iam/access-analyzer/pricing/)されます（`ValidatePolicy`は無料）。

### 複数のアカウント

ポリシーは1つのアカウントに置きますが、CDK Pipelinesの`cdk.out`は、複数のアカウントにStageをデプロイすることがよくあります。`--account <id>`を付けると、そのアカウントにデプロイするスタック（と環境を指定しないスタック）だけを使います。アカウントごとに、その認証情報で実行してください。

```sh
AWS_PROFILE=dev  npx cfn-exec-policy apply --account 111111111111
AWS_PROFILE=prod npx cfn-exec-policy apply --account 222222222222
```

`check`と`apply`は、認証情報が別のアカウントのものなら止まります。`--account`がないと、すべてのアカウントのスタックを含むポリシーを作り、警告を出します。ネストスタックのテンプレートは、同じディレクトリのスタックのアカウントに属します。それらが複数のアカウントにまたがる場合は、どのアカウントにも含めます。

## プルリクエストで使う

`diff`は、変更で実行ロールの権限がどう変わるかをレビューする人に見せます。2つの`cdk.out`から計算するので、AWSの認証情報は要りません。

```sh
npx cfn-exec-policy diff --base cdk.out.main cdk.out
```

```
execution policy: +9 actions · -13 no longer needed

+ AWS::AppMesh::Mesh (new)
    ! grants every action of the service: the type has no measured permissions
    appmesh:*

+ AWS::SQS::Queue (new)
    sqs:CreateQueue, sqs:DeleteQueue, sqs:GetQueueAttributes, sqs:GetQueueUrl, sqs:ListQueueTags,
    sqs:SetQueueAttributes, sqs:TagQueue, sqs:UntagQueue

- AWS::SNS::Topic (removed)
    iam:GetRole, sns:CreateTopic, sns:DeleteTopic, ...

13 actions no longer needed; `apply` keeps them unless --prune
```

ベース側に要る権限のどれにも含まれないアクションを「増える権限」とし、それを要るリソース型ごとに並べます。`!`は特に確かめたい追加です。権限を測っていない型の`<サービス>:*`、`iam:PassedToService`の条件がない`iam:PassRole`、そして権限を何も付けられず、デプロイに失敗する未知の型です。`--format md`でプルリクエストのコメントを出力し、`--fail-on wildcard,passrole,unknown`（またはアクションが1つでも増えたら失敗する`any`）で、該当する追加があれば終了コード`3`で終わります。`--pass-role-condition`と`--strict`を付けると、それぞれのフラグで書くポリシーの差分になります。`--strict`では、`iam:PassRole`が新しく届くロールのパターンも表示します。

GitHub Actionsでは、両方をsynthしてからアクションにコメントさせます。

```yaml
name: exec-policy
on: pull_request
permissions: { contents: read, pull-requests: write }
jobs:
  exec-policy:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
        with: { path: head }
      - uses: actions/checkout@v7
        with: { path: base, ref: "${{ github.base_ref }}" }
      - uses: actions/setup-node@v7
        with: { node-version: 22 }
      - run: npm ci && npx cdk synth -q
        working-directory: head
      - run: npm ci && npx cdk synth -q
        working-directory: base
      - uses: badmintoncryer/cfn-exec-policy@v1
        with: { base: base/cdk.out, path: head/cdk.out, fail-on: "wildcard,unknown" }
```

アクションはコメントを1つ投稿し、pushのたびにそれを更新します。同じMarkdownをジョブのサマリーにも書き、`fail-on`に当てはまればジョブを失敗させます。フォークやDependabotからのプルリクエストではトークンが読み取り専用なので、コメントできなかったと警告するだけです。

| 入力 | すること |
|---|---|
| `path` | プルリクエスト側の`cdk.out`またはテンプレート（空白区切り）。既定は`cdk.out` |
| `base` | ベースブランチ側の`cdk.out`またはテンプレート。必須 |
| `fail-on` | フラグと同じ |
| `args` | ほかのフラグ。たとえば`--pass-role-condition`や`--strict` |
| `comment` | `false`でコメントしない。既定は`true` |
| `github-token` | コメントに使うトークン。既定は`github.token` |

`@v1`は1.xの最新リリースを、`@v1.6.0`はそのバージョンを使います。

## `cdk synth`で使う

npmのパッケージには、CDKの[検証プラグイン](https://docs.aws.amazon.com/cdk/v2/guide/policy-validation-synthesis.html)も入っています。`cdk synth`のたびに`generate`を実行し、ポリシーが推測で補うものを、そのコンストラクトに報告します。

```ts
import { ExecPolicy } from 'cfn-exec-policy';

const app = new App();
// ... スタック
ExecPolicy.apply(app, { strict: true });
```

- ハンドラーの権限がない型（`<サービス>:*`を許可する）と、知らない型は警告になります。`failOnGuess: true`でエラーにできます。
- `strict: true`は、`--strict`が拒否するもの（permissions boundaryのないロール、IAMユーザーへの権限付与）があればsynthを失敗させます。自前のboundaryは`boundary`で指定します。
- `args`: ほかのフラグ。例: `['--pass-role-condition']`。

`ExecPolicy.apply`には、`Validations`のあるaws-cdk-libが要ります。古いバージョンでは、`new App({ policyValidationBeta1: [...] })`に`new ExecPolicyValidation({ ... })`を渡してください。AWSの認証情報は要りません。

## 実行に要る権限

ここで挙げるのは、`check`、`apply`、`delete`を実行する側（CLIやCIの認証情報）に要る権限です。実行ロールの権限ではありません。

| コマンド | アクション |
|---|---|
| `check`、`apply` | `cloudformation:GetTemplate`、`cloudformation:ListStackResources`（デプロイ済みのテンプレートとネストスタックを読む）、`iam:GetPolicy`、`iam:GetPolicyVersion` |
| `check --validate` | `access-analyzer:ValidatePolicy` |
| `check --no-new-access` | `access-analyzer:CheckNoNewAccess` |
| `apply --drop-unused` | `iam:GenerateServiceLastAccessedDetails`、`iam:GetServiceLastAccessedDetails` |
| `doctor` | `cloudformation:DescribeStackEvents`、`iam:GetPolicy`、`iam:GetPolicyVersion` |
| `apply`のみ | `cloudformation:DescribeStackResources`（表示するコマンドのために、bootstrapのスタックを探す）、`iam:CreatePolicy`、`iam:CreatePolicyVersion`、`iam:ListPolicyVersions`、`iam:DeletePolicyVersion`（ポリシーの版がすでに5つあるとき、既定でない最も古い版を消す） |
| `delete` | `cloudformation:DescribeStackResources`、`iam:GetPolicy`、`iam:GetPolicyVersion`、`iam:ListEntitiesForPolicy`、`iam:ListPolicyVersions`、`iam:DeletePolicyVersion`、`iam:DeletePolicy` |

`sts:GetCallerIdentity`も呼びますが、これに権限は要りません。`generate`と`--refresh-schemas`には、AWSの権限は要りません。

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": ["cloudformation:GetTemplate", "cloudformation:ListStackResources", "cloudformation:DescribeStackResources"],
      "Resource": "arn:aws:cloudformation:*:<account>:stack/*"
    },
    {
      "Effect": "Allow",
      "Action": [
        "iam:GetPolicy", "iam:GetPolicyVersion",
        "iam:CreatePolicy", "iam:CreatePolicyVersion",
        "iam:ListPolicyVersions", "iam:DeletePolicyVersion"
      ],
      "Resource": "arn:aws:iam::<account>:policy/cfn-exec-policy*"
    }
  ]
}
```

`check`だけを実行するCIのロールなら、後ろの4つのIAMアクションは外せます。`delete`を実行するなら、`iam:ListEntitiesForPolicy`と`iam:DeletePolicy`を足します。`--policy-name`を変えた場合は、Resourceもその名前に合わせます。`apply --strict`は`<policy-name>-boundary`も書き込みますが、これも同じResourceに含まれます。

`apply`を実行できる人は、実行ロールにできることを決められます。つまり、どのデプロイで何ができるかを決められます。この権限は、デプロイの仕組みをすでに管理している人だけに渡してください。なお、`cdk bootstrap`自体もIAMロールを作る権限が要ります。そのため、最初の`apply`と`cdk bootstrap`は、たいてい管理者の認証情報で実行します。

## 仕組み

CloudFormationは、リソース型ごとにスキーマを公開しています。スキーマには、作成・読み取り・更新・削除の各ハンドラが呼ぶIAMアクションが書かれています。このツールは、テンプレートに出てくるリソース型ごとに、この4つのハンドラの権限をすべて合わせて許可します。そのため、更新・置き換え・ロールバック・削除のどれも通ります。

- `check`と`apply`は、**今デプロイされている**テンプレートも読みます（ネストスタックも含む）。テンプレートから消したばかりのリソースも、削除に要る権限が残ります。
- `apply`は、ポリシーにすでにあるアクションを**消しません**。同じアカウントとリージョンにbootstrapしたアプリは、すべて1つの実行ロールを共有するためです。このポリシーをこのアプリしか使わないとわかっているときは、`--prune`を付けます。`--prune`でポリシーの分割数が減った場合、余った`cfn-exec-policy-N`は中身を空にします（新しいARNの一覧で`cdk bootstrap`をやり直すまでは、実行ロールに付いたままです）。
- タグの付け外しに要る権限は、スキーマのタグ用の欄からも足します。CDKの`Tags.of()`やスタックのタグで、タグを変えたり外したりしても通ります。
- スキーマにハンドラの権限が書かれていないリソース型が、全体の6%ほどあります。実際のアカウントで測った型（`AWS::CodeBuild::Project`、`AWS::LakeFormation::Resource`、`AWS::Route53::RecordSetGroup`など）は、手で書いた一覧を使います。それ以外（`AWS::AppMesh::Mesh`など）には`<サービス>:*`と`iam:PassRole`を付けて、警告を出します。進み具合は [#5](https://github.com/badmintoncryer/cfn-exec-policy/issues/5) にあります。
- カスタムリソースには`lambda:InvokeFunction`と`sns:Publish`を付けます。動的参照とSSMパラメータ型には、それぞれ対応する`ssm`・`secretsmanager`・`kms`の読み取り権限を付けます。
- `iam:PassRole`は、`iam:PassedToService`の条件を付けずに、`*`に対して許可します（スキーマに書かれている場合と、`<サービス>:*`で補う場合）。`--pass-role-condition`を付けると、テンプレートがロールを渡すサービスだけに絞ります。対象は、実際のアカウントで確かめた型（Lambda、Step Functions、EventBridgeのルール、ECS、S3のレプリケーション、API Gateway）です。ロールを渡すほかの型がテンプレートにあると、`iam:PassRole`は条件なしのまま残り、その型を警告で示します。
- アクションはすべてのリージョンで許可します。`--region-condition`を付けると、スタックのリージョン（`cdk.out`から読みます。環境を指定しないスタックとテンプレートファイルは`AWS_REGION`、`check`と`apply`ではCLIのリージョン）と`us-east-1`だけを許す`aws:RequestedRegion`の条件を付けます。`us-east-1`は、IAM、CloudFront、Route 53などのグローバルなサービスがリクエストを受けるリージョンです。ポリシーはアカウントの全リージョンで共有されるので、`apply`はポリシーにすでにあるリージョンを残します。ポリシーにまだリージョンの条件がなければ、ほかのリージョンのbootstrapが使っているかもしれないので、`--prune`を付けない限り止まります。足りないリージョンは`check`が示します。実際のアカウントではまだ確かめていません。`us-east-1`以外の別リージョンを呼ぶリソースはデプロイに失敗します。
- サイズの上限に収めるため、サービスごとの`Describe*` / `List*`はワイルドカードにまとめます。
- IAMの上限（6,144文字）を超えるポリシーは、`cfn-exec-policy`、`cfn-exec-policy-2`、… に分けます。

## できること、できないこと

既定のモードは、実行ロールにできることを「すべて」から「テンプレートで使うサービスだけ」に狭めます。ただし、**セキュリティの境界ではありません**。IAMロールを作れるロールは、管理者権限のロールも作れてしまうからです。

### `--strict`

`--strict`は、IAMを通じて権限を広げる経路を塞ぎます。`apply --strict`は、permissions boundaryとして`<policy-name>-boundary`も書き込みます。表示する`cdk bootstrap`のコマンドには`--custom-permissions-boundary`が付き、このboundaryが実行ロールに付きます。boundaryは、次の操作を除いてすべてを許可します。

- このboundaryのないロールを作ること。このboundaryのないロールにポリシーを付けたり追加したりすること、信頼ポリシーを変えること
- permissions boundaryを外すこと
- boundaryと`<policy-name>*`のポリシーを作ったり書き換えたりすること
- IAMユーザー、そのアクセスキーとパスワード、グループへの権限付与
- `cdk bootstrap`が作るロール（`cdk-*-role-<account>-*`。実行ロール自身を含む）を変えること

スタックが作るロールには、すべてこのboundaryが要ります。アプリ全体に付けるには、`cdk.json`に次のように書きます。

```json
"context": { "@aws-cdk/core:permissionsBoundary": { "name": "cfn-exec-policy-boundary" } }
```

boundaryは実行ロールに付きます。実行ロールは、同じアカウントとリージョンでbootstrapしたすべてのアプリが共有するため、それらのアプリすべてにこの設定が要ります。

`--strict`を付けると、ポリシーと検査も次のように変わります。

- boundaryのないロール、IAMユーザー、グループへの権限付与がテンプレートにあると、デプロイの前にエラーにします。
- `iam:CreateRole`は、boundaryを付ける場合だけ許可します。
- `iam:PassRole`で渡せるロールを、CloudFormationがスタック名から名前を付けるロール（`<スタック名>-*`。25文字を超えるスタック名は先頭25文字）、`RoleName`を固定したロール、テンプレートに書かれたロールのARNに限ります。パラメータから`RoleName`を受け取る場合など、解決できないロールは警告を出し、渡せないままにします。`--pass-role-condition`も有効になります。`--prune`を付けない`apply`は、ポリシーにすでにあるロールを残すので、実行ロールを共有するほかのアプリも自分のロールを渡せます。足りないロールは`check`が示します。
- 権限が分かっていないリソースの型は、`<サービス>:*`で補わずにエラーにします。
- `*`や`iam:*`のように、すべてのロールに対する`iam:PassRole`と`iam:CreateRole`を許すアクションがポリシーにすでにあると、`apply`はエラーで止まります。
- スタック名が要るため、入力は`cdk.out`のディレクトリに限ります。テンプレートのファイルを渡すとエラーになります。

`--boundary <名前またはARN>`を付けると、自分で用意したboundaryを使います。この場合、`apply`はboundaryを書き込みません。

boundaryはIAM以外の操作をすべて許可します。そのため`--strict`を付けても、スタックが作ったロールは、そのロール自身のポリシーが許す範囲でデータを読んだり、リソースを消したりできます。また、boundaryの付いたロールは、boundaryのない既存のロールをサービスに渡せます。`iam:PassRole`には、boundaryを判定する条件キーがないためです。範囲を絞れるのは、実行ロール自身の`iam:PassRole`だけです。`check`は足りないアクションやロールには気づきますが、`Resource`や`Condition`が広げられても気づきません。`check --no-new-access`なら気づきます。

`--strict`を付ける前にデプロイしたロールには、次のデプロイでboundaryが付きます。同じデプロイでインラインポリシーや管理ポリシーを変えても、デプロイは通ります（実際のアカウントで確かめました）。詳しくは[docs/design.md](docs/design.md)を見てください。

## 使うのをやめるとき

先に実行ロールを`AdministratorAccess`に戻します（bootstrapのスタックを消してもかまいません）。IAMは、ロールに付いたままのポリシーを削除できないためです。そのあとで、ポリシーを削除します。

```sh
cdk bootstrap aws://123456789012/us-east-1 --cloudformation-execution-policies arn:aws:iam::aws:policy/AdministratorAccess
npx cfn-exec-policy delete         # 削除するポリシーを表示する
npx cfn-exec-policy delete --yes
```

`delete`は、分割したポリシー（`cfn-exec-policy-2`、…）と、`apply`のたびに増える古いバージョンも消します。IAMは、古いバージョンが残っているポリシーを削除できないためです。どれかがまだロールに付いていれば、何も消さずに上の`cdk bootstrap`のコマンドを表示します。

`apply --strict`を使っていた場合、`delete`は`<policy-name>-boundary`も消します（`--boundary`で渡した自前のboundaryは消しません）。IAMは、ロールに付いたままのboundaryを削除できません。そのため、先にロールから外します。boundaryは実行ロールがboundaryを外すことを禁じるので、実行ロールから先に外します。

1. 実行ロール: `cdk bootstrap`は、`--custom-permissions-boundary`を省くと前のboundaryをそのまま残します。`Removing existing permissions boundary`と表示されますが、boundaryは外れません。そのため、bootstrapのスタックのパラメータ`InputPermissionsBoundary`を空にします（CloudFormationのコンソールで「スタックの更新」→「既存のテンプレートを使用」）。bootstrapのスタックを消してもかまいません。
2. スタックが作ったロール: `cdk.json`から`@aws-cdk/core:permissionsBoundary`を消して、各アプリをデプロイし直します（または削除します）。

boundaryがまだ付いているロールは、`delete`が表示します。

## AIエージェントから使う

`cfn-exec-policy mcp`は、標準入出力で[Model Context Protocol](https://modelcontextprotocol.io)のサーバーとして動きます。CDKを書くエージェントが、変更が実行ロールに何を求めるかを確かめられます。

| ツール | 実行する内容 | AWSの認証情報 |
|---|---|---|
| `exec_policy_explain` | `generate --explain --format json`：アクションごとに、それが要るリソース | 不要 |
| `exec_policy_generate` | `generate`：ポリシードキュメント | 不要 |
| `exec_policy_diff` | `diff --format md --base <base>`：変更で増える権限と減る権限 | 不要 |
| `exec_policy_doctor` | `doctor <stack> --format json`：失敗したデプロイの権限エラー | 要（読み取りのみ） |

テンプレートを読むツールは、`inputs`（既定はクライアントの作業ディレクトリの`./cdk.out`）と、`strict`、`pass_role_condition`、`scope_resources`を受け取ります。Claude Codeなら次のとおりです。

```sh
claude mcp add exec-policy -- npx -y cfn-exec-policy mcp
```

ほかのクライアントでは、MCPの設定に次を書きます。

```json
{ "mcpServers": { "exec-policy": { "command": "npx", "args": ["-y", "cfn-exec-policy", "mcp"] } } }
```

## インストール

```sh
npx cfn-exec-policy …                                    # npm（npx cdk-exec-policy でも可）
go install github.com/badmintoncryer/cfn-exec-policy@latest
```

Releasesからバイナリをダウンロードすることもできます。

## ライセンス

Apache-2.0
