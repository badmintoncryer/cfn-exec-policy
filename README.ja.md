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

表示された`cdk bootstrap`のコマンドを1回実行すれば、その環境のデプロイはすべて、管理者権限ではなく生成したポリシーで行われます。

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
| `generate [入力]` | ポリシーのJSONを表示する | 不要 |
| `check [入力]` | テンプレートに要る権限が管理ポリシーに足りなければ、終了コード1で終わる。`cdk deploy`の前にCIで実行する。ポリシーは名前で読むだけで、実行ロールに付いているかまでは確かめない | 要（読み取りのみ） |
| `apply [入力]` | 管理ポリシーを作成または更新し、`cdk bootstrap`のコマンドを表示する | 要 |

入力には、`cdk.out`のディレクトリか、CloudFormationのテンプレート（JSONまたはYAML）を渡します。省略すると`./cdk.out`を読みます。ネストスタックとStageも含めて読みます。

フラグ: `--policy-name`（既定は`cfn-exec-policy`）、`--refresh-schemas`（同梱の表ではなく、最新のCloudFormationスキーマを使う）、`--pass-role-condition`（`iam:PassRole`を渡し先のサービスで絞る。後述）、`apply --prune`。

## 仕組み

CloudFormationは、リソース型ごとにスキーマを公開しています。スキーマには、作成・読み取り・更新・削除の各ハンドラが呼ぶIAMアクションが書かれています。このツールは、テンプレートに出てくるリソース型ごとに、この4つのハンドラの権限をすべて合わせて許可します。そのため、更新・置き換え・ロールバック・削除のどれも通ります。

- `check`と`apply`は、**今デプロイされている**テンプレートも読みます（ネストスタックも含む）。テンプレートから消したばかりのリソースも、削除に要る権限が残ります。
- `apply`は、ポリシーにすでにあるアクションを**消しません**。同じアカウントとリージョンにbootstrapしたアプリは、すべて1つの実行ロールを共有するためです。このポリシーをこのアプリしか使わないとわかっているときは、`--prune`を付けます。`--prune`でポリシーの分割数が減った場合、余った`cfn-exec-policy-N`は中身を空にします（新しいARNの一覧で`cdk bootstrap`をやり直すまでは、実行ロールに付いたままです）。
- タグの付け外しに要る権限は、スキーマのタグ用の欄からも足します。CDKの`Tags.of()`やスタックのタグで、タグを変えたり外したりしても通ります。
- スキーマにハンドラの権限が書かれていないリソース型が、全体の7%ほどあります。実際のアカウントで測った型（`AWS::CodeBuild::Project`、`AWS::Glue::Table`、`AWS::Route53::RecordSetGroup`など）は、手で書いた一覧を使います。それ以外（`AWS::EMR::Cluster`など）には`<サービス>:*`と`iam:PassRole`を付けて、警告を出します。進み具合は [#5](https://github.com/badmintoncryer/cfn-exec-policy/issues/5) にあります。
- カスタムリソースには`lambda:InvokeFunction`と`sns:Publish`を付けます。動的参照とSSMパラメータ型には、それぞれ対応する`ssm`・`secretsmanager`・`kms`の読み取り権限を付けます。
- `iam:PassRole`は、`iam:PassedToService`の条件を付けずに、`*`に対して許可します（スキーマに書かれている場合と、`<サービス>:*`で補う場合）。`--pass-role-condition`を付けると、テンプレートがロールを渡すサービスだけに絞ります。対象は、実際のアカウントで確かめた型（Lambda、Step Functions、EventBridgeのルール、ECS、S3のレプリケーション、API Gateway）です。ロールを渡すほかの型がテンプレートにあると、`iam:PassRole`は条件なしのまま残り、その型を警告で示します。
- サイズの上限に収めるため、サービスごとの`Describe*` / `List*`はワイルドカードにまとめます。
- IAMの上限（6,144文字）を超えるポリシーは、`cfn-exec-policy`、`cfn-exec-policy-2`、… に分けます。

## できること、できないこと

既定のモードは、実行ロールにできることを「すべて」から「テンプレートで使うサービスだけ」に狭めます。ただし、**セキュリティの境界ではありません**。IAMロールを作れるロールは、管理者権限のロールも作れてしまうからです。これを塞ぐ仕組み（スタックが作るすべてのロールにpermissions boundaryを付ける）は、`--strict`として追加する予定です。詳しくは[docs/design.md](docs/design.md)を見てください。

## インストール

```sh
npx cfn-exec-policy …                                    # npm（npx cdk-exec-policy でも可）
go install github.com/badmintoncryer/cfn-exec-policy@latest
```

Releasesからバイナリをダウンロードすることもできます。

## ライセンス

Apache-2.0
