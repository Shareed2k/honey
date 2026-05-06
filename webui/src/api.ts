const TOKEN_KEY = 'honey_web_token';

export function getToken(): string {
  const q = new URLSearchParams(window.location.search).get('token');
  if (q) {
    sessionStorage.setItem(TOKEN_KEY, q);
    return q;
  }
  return sessionStorage.getItem(TOKEN_KEY) || '';
}

export function apiHeaders(): HeadersInit {
  const t = getToken();
  const h: Record<string, string> = { Accept: 'application/json' };
  if (t) {
    h.Authorization = `Bearer ${t}`;
    h['X-Honey-Token'] = t;
  }
  return h;
}

export async function apiGet(path: string): Promise<Response> {
  return fetch(path, { headers: apiHeaders() });
}

export async function apiPost(path: string, body: unknown): Promise<Response> {
  return fetch(path, {
    method: 'POST',
    headers: { ...apiHeaders(), 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
}

export async function apiPut(path: string, body: BodyInit, contentType?: string): Promise<Response> {
  const h: Record<string, string> = { ...apiHeaders() } as Record<string, string>;
  if (contentType) {
    h['Content-Type'] = contentType;
  }
  return fetch(path, { method: 'PUT', headers: h, body });
}

export async function apiPutJson(path: string, body: unknown): Promise<Response> {
  return fetch(path, {
    method: 'PUT',
    headers: { ...apiHeaders(), 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
}

export async function apiDelete(path: string): Promise<Response> {
  return fetch(path, { method: 'DELETE', headers: apiHeaders() });
}

/** POST multipart FormData with upload progress (bytes to this origin only). Resolves parsed JSON body. */
export function uploadFormDataWithProgress(
  url: string,
  formData: FormData,
  onProgress: (percent: number) => void,
): Promise<unknown> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open('POST', url);
    const h = apiHeaders() as Record<string, string>;
    for (const [k, v] of Object.entries(h)) {
      xhr.setRequestHeader(k, v);
    }
    xhr.upload.onprogress = (ev) => {
      if (ev.lengthComputable && ev.total > 0) {
        onProgress(Math.round((100 * ev.loaded) / ev.total));
      }
    };
    xhr.onload = () => {
      let body: unknown;
      try {
        body = JSON.parse(xhr.responseText) as unknown;
      } catch {
        body = xhr.responseText;
      }
      if (xhr.status >= 200 && xhr.status < 300) {
        resolve(body);
      } else {
        let msg = xhr.statusText || `HTTP ${xhr.status}`;
        if (typeof body === 'object' && body !== null && 'error' in body) {
          msg = String((body as { error: string }).error);
        } else if (typeof body === 'string' && body.trim()) {
          msg = body.trim().slice(0, 800);
        }
        reject(new Error(msg));
      }
    };
    xhr.onerror = () => reject(new Error('network error'));
    xhr.send(formData);
  });
}
