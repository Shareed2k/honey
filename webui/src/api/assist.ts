import { apiGet, apiPost } from './core';



export async function recipeAssist(body: {
  recipe_path: string;
  model: string;
  user_prompt?: string;
  ssh_user?: string;
  records?: unknown[];
}): Promise<{ reply: string }> {
  const r = await apiPost('/api/v1/recipes/assist', body);
  const j = (await r.json().catch(() => ({}))) as { reply?: string; error?: string };
  if (!r.ok) {
    throw new Error(j.error || r.statusText);
  }
  return { reply: (j.reply || '').trim() };
}

export async function fetchTerminalAssistModels(): Promise<string[]> {
  const r = await apiGet('/api/v1/terminal-assist/models');
  const j = (await r.json().catch(() => ({}))) as { models?: string[]; error?: string };
  if (!r.ok) {
    throw new Error(j.error || r.statusText);
  }
  return j.models || [];
}