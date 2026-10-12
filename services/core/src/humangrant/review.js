(() => {
  'use strict';

  const root = document.getElementById('human-grant');
  if (!root) return;
  const byId = (id) => document.getElementById(id);
  const status = byId('status');
  const setStatus = (message) => { status.textContent = message; };
  const api = async (url, options = {}) => {
    const response = await fetch(url, { cache: 'no-store', credentials: 'same-origin', ...options });
    const data = await response.json();
    if (!response.ok) throw new Error(data.message || data.error || 'Request denied');
    return data;
  };
  const securePost = (path, access, csrf, input) => api(path, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      'Authorization': 'Bearer ' + access,
      ...(csrf ? { 'X-HumanGrant-CSRF': csrf } : {}),
    },
    body: JSON.stringify(input),
  });

  // The browser's refresh token remains HttpOnly. The short-lived human bearer
  // exists only in this page's memory; never forward or display it to an agent.
  const signIn = async () => {
    const data = await api('/api/user/auth/refresh', { method: 'POST' });
    const access = data && data.data && data.data.access_token;
    if (typeof access !== 'string' || !access) {
      throw new Error('Please sign in to LMM API in this browser first.');
    }
    return access;
  };

  const showData = (data) => {
    const list = byId('details');
    list.replaceChildren();
    for (const [title, value] of [
      ['Requester key', data.actor_id],
      ['Application', data.client_id],
      ['Service', data.target_service],
      ['Current request state', data.phase],
    ]) {
      const dt = document.createElement('dt');
      const dd = document.createElement('dd');
      dt.textContent = title;
      dd.textContent = String(value);
      list.append(dt, dd);
    }
    byId('purpose').textContent = 'AI-provided purpose (untrusted): ' + data.purpose;
    byId('expires').textContent = 'This request expires at ' +
      new Date(data.expires_at * 1000).toLocaleString();
  };

  async function review() {
    const link = window.location.pathname.slice('/human/'.length);
    const path = '/human/' + encodeURIComponent(link);
    let details;
    try {
      details = await api(path + '/details');
    } catch (error) {
      setStatus(error.message);
      return;
    }
    showData(details);
    byId('request').hidden = false;
    setStatus('Review the request before authorizing.');
    const permissions = byId('permissions');
    const boxes = [];
    for (const permission of details.permissions) {
      const label = document.createElement('label');
      const checkbox = document.createElement('input');
      checkbox.type = 'checkbox';
      checkbox.checked = false;
      label.append(checkbox, document.createTextNode(
        ' ' + permission.action + ' on ' + permission.resource));
      permissions.append(label);
      boxes.push([checkbox, permission]);
    }

    let access = null;
    let csrf = null;
    const begin = byId('begin');
    const approve = byId('approve');
    const reject = byId('reject');
    const updateApproval = () => {
      approve.disabled = !csrf || !boxes.some(([box]) => box.checked);
    };
    for (const [checkbox] of boxes) checkbox.addEventListener('change', updateApproval);

    begin.addEventListener('click', async () => {
      begin.disabled = true;
      try {
        access = await signIn();
        const session = await securePost(path + '/begin', access, null, {});
        csrf = session.csrf_token;
        byId('identity').textContent = 'You are signed in as ' +
          session.username + ' (account ' + session.grantor_id + ').';
        setStatus('Choose exact permissions. Nothing is granted until you approve.');
        reject.disabled = false;
        updateApproval();
      } catch (error) {
        setStatus(error.message + ' Sign in to LMM API, then try again.');
        begin.disabled = false;
      }
    });
    approve.addEventListener('click', async () => {
      approve.disabled = true;
      reject.disabled = true;
      try {
        const selected = boxes.filter(([box]) => box.checked).map(([, permission]) => permission);
        if (!selected.length) throw new Error('Select a permission.');
        const ttl = Math.max(30, Math.min(600, details.requested_ttl_seconds || 3600) - 30);
        await securePost(path + '/approve', access, csrf, {
          permissions: selected,
          expires_at: Math.floor(Date.now() / 1000) + ttl,
        });
        setStatus('Approved. The AI must complete its signed credential claim.');
      } catch (error) {
        setStatus(error.message);
        reject.disabled = false;
        updateApproval();
      }
    });
    reject.addEventListener('click', async () => {
      approve.disabled = true;
      reject.disabled = true;
      try {
        await securePost(path + '/reject', access, csrf, {});
        setStatus('Request rejected. No access was granted.');
      } catch (error) {
        setStatus(error.message);
        reject.disabled = false;
        updateApproval();
      }
    });
  }

  async function grants() {
    const panel = byId('grant-list');
    panel.hidden = false;
    try {
      const access = await signIn();
      const response = await api('/api/humangrant/grants', {
        headers: { Authorization: 'Bearer ' + access },
      });
      panel.replaceChildren();
      setStatus(response.grants.length ? 'Current and past grants' : 'No grants found.');
      for (const grant of response.grants) {
        const card = document.createElement('article');
        const title = document.createElement('strong');
        title.textContent = 'AI key: ' + grant.actor_id;
        const summary = document.createElement('p');
        summary.textContent = grant.permissions.map(p => p.action + ' on ' + p.resource).join(', ') +
          ' · expires ' + new Date(grant.expires_at * 1000).toLocaleString();
        const button = document.createElement('button');
        button.textContent = grant.revoked ? 'Revoked' : 'Revoke access';
        button.disabled = grant.revoked || !grant.csrf_token;
        button.addEventListener('click', async () => {
          button.disabled = true;
          try {
            await securePost('/api/humangrant/grants/' + encodeURIComponent(grant.grant_id) +
              '/revoke', access, grant.csrf_token, {});
            button.textContent = 'Revoked';
            setStatus('Grant revoked.');
          } catch (error) {
            button.disabled = false;
            setStatus(error.message);
          }
        });
        card.append(title, summary, button);
        panel.append(card);
      }
    } catch (error) {
      setStatus(error.message + ' Sign in to LMM API in this browser.');
    }
  }

  if (root.dataset.page === 'intro') {
    setStatus('To authorize an AI, ask it to create a signed HumanGrant request. Open the human_authorization_url that the AI shares. You must sign in and approve each permission yourself.');
    const link = document.createElement('a');
    link.href = '/human/grants';
    link.textContent = 'Review and revoke my existing AI grants';
    byId('grant-list').hidden = false;
    byId('grant-list').append(link);
  }
  if (root.dataset.page === 'review') review();
  if (root.dataset.page === 'grants') grants();
})();
