import { pathToFileURL } from 'node:url';
import assert from 'node:assert/strict';

const { default: OpenAI } = await import(pathToFileURL(process.argv[2]).href);
const client = new OpenAI({ apiKey: 'gateway-test-token', baseURL: `${process.argv[3]}/gateway/openai-chat-completions`, maxRetries: 0 });
const tools = [{ type: 'function', function: { name: 'lookup', parameters: { type: 'object', properties: { value: { type: 'string' } }, required: ['value'] } } }];
for (const stream of [false, true]) {
  const messages = [{ role: 'user', content: 'Read the local fixture.' }];
  const headers = { 'x-elysia-session-id': `openai-sdk-${stream}` };
  const first = stream
    ? await client.chat.completions.stream({ model: 'group', stream_options: { include_usage: true }, messages, tools }, { headers }).finalChatCompletion()
    : await client.chat.completions.create({ model: 'group', messages, tools }, { headers });
  assert.equal(first.choices[0].message.tool_calls.length, 1);
  assert.equal(first.usage.total_tokens, 5);
  const message = first.choices[0].message;
  // Project the SDK's enriched parsed result back to the Chat request schema.
  // Deliberately omit the gateway extension in the streaming case to exercise
  // clients that keep only standard fields, using exact persistent recovery.
  messages.push(stream ? {
    role: 'assistant', content: message.content,
    tool_calls: message.tool_calls.map(({ id, type, function: fn }) => ({ id, type, function: { name: fn.name, arguments: fn.arguments } })),
  } : message);
  messages.push({ role: 'tool', tool_call_id: 'client-call', content: 'LOCAL_CLIENT_FIXTURE' });
  // SDK stream aggregation may discard unknown extensions. A stable session
  // supplies the independently tested exact persistent recovery channel.
  const second = await client.chat.completions.create({ model: 'group', messages, tools }, { headers });
  assert.equal(second.choices[0].message.content, 'LOCAL_CLIENT_OK');
}
console.log('LOCAL_CLIENT_OK');
