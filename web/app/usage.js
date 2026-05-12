import { clamp, num } from "./format.js";

export function compositionFor(
  inputTokens,
  cachedTokens,
  outputTokens,
  composition,
) {
  if (composition) return composition;
  var input = num(inputTokens);
  var cached = num(cachedTokens);
  var output = num(outputTokens);
  var total = input + cached + output;
  return {
    cached_input: {
      tokens: cached,
      percent: total ? (cached / total) * 100 : 0,
    },
    input: { tokens: input, percent: total ? (input / total) * 100 : 0 },
    output: { tokens: output, percent: total ? (output / total) * 100 : 0 },
  };
}

export function part(comp, key) {
  return comp && comp[key] ? comp[key] : { tokens: 0, percent: 0 };
}

export function totalInput(account) {
  return num(account.input_tokens) + num(account.cached_tokens);
}

export function accountTokens(account) {
  return Array.isArray(account.token_ids) ? account.token_ids : [];
}

export function displayAccountName(account) {
  var accountKey = String(account.account_key || "");
  return (
    account.email ||
    (accountKey.toLowerCase().endsWith(".json") ? "" : accountKey) ||
    account.account_id ||
    account.user_id ||
    "Unknown account"
  );
}

export function accountInitial(account) {
  var source = displayAccountName(account).trim();
  return (source[0] || "?").toUpperCase();
}

export function quotaProgress(used, limit) {
  if (!limit || limit <= 0) return 0;
  return clamp((used / limit) * 100, 0, 100);
}
