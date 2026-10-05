export const handler = async () => ({ statusCode: 200, body: process.env.MESSAGE ?? "ok" });
