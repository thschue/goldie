import { get } from '../api/client';
import type { components } from '../api/schema';
import { api } from '../auth/session';

export type Article = components['schemas']['Article'];
export type ArticleList = components['schemas']['ArticleList'];

// listArticles reads the articles that win in a Project's view (GET /api/v1/articles). An empty
// Project is no `project` parameter: the API answers the global view (a blank value is a 400).
export function listArticles(project: string, signal?: AbortSignal) {
  const q = project ? `?${new URLSearchParams({ project })}` : '';
  return get<ArticleList>(`/api/v1/articles${q}`, signal);
}

// An API error as the person may see it: the envelope's message and hint, never
// the body of a reply that is not an envelope and never a transport error.
export class ArticleError extends Error {
  hint: string;
  constructor(message: string, hint = '') {
    super(message);
    this.hint = hint;
  }
}

const generic = "Couldn't load this article. Try again in a moment.";

// getArticle reads one article (GET /api/v1/articles/{id}). The id is one path
// segment, so a `/` inside it travels as %2F (ADR 0016). Fields the server did
// not send stay absent: an absent `enforced` or `overrides` means unknown (#529).
export async function getArticle(id: string): Promise<Article> {
  let res: Response;
  try {
    res = await api(`/api/v1/articles/${encodeURIComponent(id)}`);
  } catch {
    throw new ArticleError(generic);
  }
  let body: unknown;
  try {
    body = await res.json();
  } catch {
    body = undefined;
  }
  if (!res.ok) {
    const e = (body as components['schemas']['ErrorEnvelope'] | undefined)?.error;
    const message = typeof e?.message === 'string' && e.message !== '' ? e.message : generic;
    const hint = typeof e?.details?.hint === 'string' ? e.details.hint : '';
    throw new ArticleError(message, hint);
  }
  const a = body as Partial<Article> | undefined;
  if (!a || typeof a.id !== 'string' || typeof a.name !== 'string' || typeof a.content !== 'string' || !Array.isArray(a.keywords)) {
    throw new ArticleError(generic);
  }
  return a as Article;
}
