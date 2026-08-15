const state = { token: sessionStorage.getItem('cronix_token') || '' };

const $ = (id) => document.getElementById(id);

function authHeaders() {
  const h = { 'Content-Type': 'application/json' };
  if (state.token) h['Authorization'] = 'Bearer ' + state.token;
  return h;
}

async function api(method, path, body) {
  const res = await fetch(path, {
    method,
    headers: authHeaders(),
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (res.status === 401) {
    state.token = '';
    sessionStorage.removeItem('cronix_token');
    render();
    throw new Error('unauthorized');
  }
  if (!res.ok) {
    let msg = 'request failed: ' + res.status;
    try {
      const j = await res.json();
      if (j && j.error) msg = j.error;
    } catch (_) {}
    throw new Error(msg);
  }
  if (res.status === 204) return null;
  return res.json();
}

function render() {
  const authed = !!state.token;
  $('app').hidden = !authed;
  $('login').hidden = authed;
  $('token').value = state.token;
  if (authed) loadJobs();
}

async function loadJobs() {
  try {
    const jobs = await api('GET', '/api/v1/jobs');
    const tbody = $('jobs').querySelector('tbody');
    tbody.innerHTML = '';
    for (const j of jobs) {
      const tr = document.createElement('tr');
      tr.innerHTML = `<td>${escapeHtml(j.id)}</td><td>${escapeHtml(j.name || '')}</td>`
        + `<td><input type="checkbox" data-id="${escapeHtml(j.id)}" class="toggle" ${j.enabled ? 'checked' : ''}></td>`
        + `<td>${escapeHtml(j.schedule)}</td>`;
      const actions = document.createElement('td');
      const edit = document.createElement('button'); edit.textContent = 'edit'; edit.dataset.id = j.id;
      const run = document.createElement('button'); run.textContent = 'run'; run.dataset.id = j.id;
      const del = document.createElement('button'); del.textContent = 'delete'; del.dataset.id = j.id;
      actions.append(edit, run, del);
      tr.appendChild(actions);
      tbody.appendChild(tr);
    }
    showError('');
  } catch (e) {
    showError(e.message);
  }
}

async function toggleJob(id, enabled) {
  const jobs = await api('GET', '/api/v1/jobs');
  const j = jobs.find((x) => x.id === id);
  if (!j) return;
  j.enabled = enabled;
  await api('PUT', '/api/v1/jobs/' + encodeURIComponent(id), {
    name: j.name, schedule: j.schedule, curl: j.curl,
    retries: j.retries, retry_delay: j.retry_delay, enabled,
  });
  loadJobs();
}

async function openEdit(job) {
  $('editor').hidden = false;
  $('editor-title').textContent = job ? 'Edit job' : 'New job';
  $('field-id').value = job ? job.id : '';
  $('field-name').value = job ? job.name || '' : '';
  $('field-schedule').value = job ? job.schedule : '';
  $('field-curl').value = job ? job.curl : '';
  $('field-retries').value = job ? job.retries : 0;
  $('field-retry-delay').value = job ? job.retry_delay : 5;
  $('field-enabled').checked = job ? job.enabled : true;
  $('field-errors').hidden = true;
}

async function saveJob(e) {
  e.preventDefault();
  const job = {
    name: $('field-name').value,
    schedule: $('field-schedule').value,
    curl: $('field-curl').value,
    retries: parseInt($('field-retries').value, 10),
    retry_delay: parseInt($('field-retry-delay').value, 10),
    enabled: $('field-enabled').checked,
  };
  const id = $('field-id').value;
  try {
    if (id) {
      await api('PUT', '/api/v1/jobs/' + encodeURIComponent(id), job);
    } else {
      await api('POST', '/api/v1/jobs', job);
    }
    $('editor').hidden = true;
    loadJobs();
  } catch (e) {
    $('field-errors').textContent = e.message;
    $('field-errors').hidden = false;
  }
}

async function runJob(id) {
  try {
    const res = await api('POST', '/api/v1/jobs/' + encodeURIComponent(id) + '/run');
    const lines = (res.steps || []).map((s) =>
      `attempt=${s.attempt}/${s.total} exit=${s.exit} output=${s.output}`).join('\n');
    $('run-output').textContent = lines || '(no output)';
    $('run-panel').hidden = false;
  } catch (e) {
    showError(e.message);
  }
}

function escapeHtml(s) {
  return s.replace(/[&<>"']/g, (c) => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
  }[c]));
}

function showError(msg) {
  $('error').textContent = msg;
  $('error').hidden = !msg;
}

$('save-token').addEventListener('click', () => {
  state.token = $('token').value.trim();
  sessionStorage.setItem('cronix_token', state.token);
  render();
});
$('new-job').addEventListener('click', () => openEdit(null));
$('editor-cancel').addEventListener('click', () => { $('editor').hidden = true; });
$('run-close').addEventListener('click', () => { $('run-panel').hidden = true; });
document.getElementById('job-form').addEventListener('submit', saveJob);

document.addEventListener('change', (e) => {
  if (e.target.classList.contains('toggle')) toggleJob(e.target.dataset.id, e.target.checked);
});
document.addEventListener('click', async (e) => {
  const edit = e.target.closest('button[data-id]');
  if (!edit) return;
  if (edit.textContent === 'edit') {
    const j = (await api('GET', '/api/v1/jobs')).find((x) => x.id === edit.dataset.id);
    openEdit(j);
  } else if (edit.textContent === 'run') {
    runJob(edit.dataset.id);
  } else if (edit.textContent === 'delete') {
    await api('DELETE', '/api/v1/jobs/' + encodeURIComponent(edit.dataset.id));
    loadJobs();
  }
});

render();