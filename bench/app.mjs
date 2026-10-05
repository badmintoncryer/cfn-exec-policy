// Stacks deployed with an execution role holding only the generated policy (issue #1).
// -c phase=2 updates every stack: replacements, a changed custom resource, and the SNS
// topic removed (no other stack has SNS, so its delete permissions come only from the
// deployed template).
import * as cdk from "aws-cdk-lib";
import { aws_apigateway as apigw, aws_cloudwatch as cw, aws_dynamodb as ddb, aws_ec2 as ec2,
  aws_ecs as ecs, aws_ecs_patterns as ecsp, aws_events as events, aws_events_targets as targets,
  aws_lambda as lambda, aws_logs as logs, aws_s3 as s3, aws_sns as sns, aws_sqs as sqs,
  aws_ssm as ssm, aws_stepfunctions as sfn, custom_resources as cr } from "aws-cdk-lib";

const app = new cdk.App();
const phase = Number(app.node.tryGetContext("phase") ?? 1);
const env = { account: "214794239830", region: "us-east-1" };
const DESTROY = cdk.RemovalPolicy.DESTROY;

// Lambda (asset from the bootstrap bucket) + API Gateway
{
  const s = new cdk.Stack(app, "cfnxp-api", { env });
  const fn = new lambda.Function(s, "Fn", {
    runtime: lambda.Runtime.NODEJS_22_X, handler: "index.handler",
    code: lambda.Code.fromAsset("fn"), environment: { MESSAGE: `phase ${phase}` },
  });
  new apigw.LambdaRestApi(s, "Api", { handler: fn, cloudWatchRole: false });
}

// ECS Fargate behind an ALB, on the default VPC
{
  const s = new cdk.Stack(app, "cfnxp-ecs", { env });
  const vpc = ec2.Vpc.fromVpcAttributes(s, "Vpc", {
    vpcId: "vpc-4331593e", availabilityZones: ["us-east-1a", "us-east-1b"],
    publicSubnetIds: ["subnet-2e0a500f", "subnet-ab11fde7"],
  });
  new ecsp.ApplicationLoadBalancedFargateService(s, "Svc", {
    vpc, assignPublicIp: true, taskSubnets: { subnetType: ec2.SubnetType.PUBLIC },
    desiredCount: 1, cpu: 256, memoryLimitMiB: 512,
    taskImageOptions: { image: ecs.ContainerImage.fromRegistry("public.ecr.aws/nginx/nginx:stable"),
      environment: { PHASE: String(phase) } },
  });
}

// Misc types; DynamoDB key change forces a replacement in phase 2
{
  const s = new cdk.Stack(app, "cfnxp-misc", { env });
  const q = new sqs.Queue(s, "Queue");
  if (phase === 1) new sns.Topic(s, "Topic");
  new ddb.Table(s, "Table", {
    partitionKey: { name: phase === 1 ? "pk" : "id", type: ddb.AttributeType.STRING },
    billingMode: ddb.BillingMode.PAY_PER_REQUEST, removalPolicy: DESTROY,
  });
  new sfn.StateMachine(s, "Machine", { definitionBody: sfn.DefinitionBody.fromChainable(new sfn.Pass(s, "Pass")) });
  new events.Rule(s, "Rule", { schedule: events.Schedule.rate(cdk.Duration.days(1)), targets: [new targets.SqsQueue(q)] });
  new ssm.StringParameter(s, "Ami", {
    stringValue: ssm.StringParameter.valueForStringParameter(s, "/aws/service/ami-amazon-linux-latest/al2023-ami-kernel-default-x86_64"),
  });
  new cw.Alarm(s, "Alarm", { metric: q.metricApproximateNumberOfMessagesVisible(), threshold: 100, evaluationPeriods: 1 });
}

// Nested stack + custom resources (AwsCustomResource, auto-delete bucket)
{
  const s = new cdk.Stack(app, "cfnxp-nested", { env });
  const n = new cdk.NestedStack(s, "Inner");
  new sqs.Queue(n, "InnerQueue");
  new s3.Bucket(s, "Bucket", { autoDeleteObjects: true, removalPolicy: DESTROY });
  new cr.AwsCustomResource(s, "WhoAmI", {
    onUpdate: { service: "STS", action: "GetCallerIdentity", parameters: {},
      physicalResourceId: cr.PhysicalResourceId.of(`whoami-${phase}`) },
    policy: cr.AwsCustomResourcePolicy.fromSdkCalls({ resources: cr.AwsCustomResourcePolicy.ANY_RESOURCE }),
    installLatestAwsSdk: false,
  });
}

// Stage
{
  const stage = new cdk.Stage(app, "cfnxp-stage", { env });
  const s = new cdk.Stack(stage, "Svc");
  const lg = new logs.LogGroup(s, "Logs", { removalPolicy: DESTROY, retention: logs.RetentionDays.ONE_DAY });
  new lambda.Function(s, "Fn", {
    runtime: lambda.Runtime.NODEJS_22_X, handler: "index.handler",
    code: lambda.Code.fromInline(`exports.handler = async () => ${phase}`), logGroup: lg,
  });
}
