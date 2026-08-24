(() => {
  const root = document.querySelector('[data-upload-root]')
  if (!root) return

  const picker = root.querySelector('[data-upload-picker]')
  const selectButton = root.querySelector('[data-upload-select]')
  const dropZone = root.querySelector('[data-upload-drop]')
  const status = root.querySelector('[data-upload-status]')
  const progress = root.querySelector('[data-upload-progress]')
  const csrf = root.dataset.uploadCsrf
  const concurrency = Math.max(1, Number(root.dataset.uploadConcurrency) || 3)
  let resumeSessionID = ''

  const entryFile = (entry) => new Promise((resolve, reject) => entry.file(resolve, reject))
  const readEntries = (reader) => new Promise((resolve, reject) => reader.readEntries(resolve, reject))

  async function walkEntry(entry, prefix, output) {
    const relative = prefix ? `${prefix}/${entry.name}` : entry.name
    if (entry.isFile) {
      output.push({ file: await entryFile(entry), path: relative })
      return
    }
    const reader = entry.createReader()
    for (;;) {
      const children = await readEntries(reader)
      if (children.length === 0) return
      for (const child of children) await walkEntry(child, relative, output)
    }
  }

  async function collectFiles(event) {
    const output = []
    const items = [...(event.dataTransfer?.items || [])]
    if (items.length) {
      for (const item of items) {
        const entry = item.webkitGetAsEntry?.()
        if (entry) await walkEntry(entry, '', output)
      }
      return output
    }
    return [...event.target.files].map((file) => ({ file, path: file.webkitRelativePath || file.name }))
  }

  async function createSession(files) {
    const response = await fetch('/upload-sessions', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf },
      body: JSON.stringify({ files: files.map(({ file, path }) => ({ relative_path: path, size_bytes: file.size, media_type: file.type })) }),
    })
    if (!response.ok) throw new Error(await response.text())
    return response.json()
  }

  async function getSession(sessionID) {
    const response = await fetch(`/upload-sessions/${sessionID}`)
    if (!response.ok) throw new Error(await response.text())
    return response.json()
  }

  function uploadEntry(sessionID, entry, onProgress) {
    return new Promise((resolve, reject) => {
      const request = new XMLHttpRequest()
      request.open('PUT', `/upload-sessions/${sessionID}/files/${entry.id}`)
      request.setRequestHeader('X-CSRF-Token', csrf)
      request.upload.onprogress = ({ loaded, total }) => onProgress(loaded, total)
      request.onload = () => request.status >= 200 && request.status < 300 ? resolve() : reject(new Error(request.responseText))
      request.onerror = () => reject(new Error('upload request failed'))
      request.send(entry.file)
    })
  }

  async function runPool(entries, poolSize, worker) {
    let next = 0
    await Promise.all(Array.from({ length: Math.min(poolSize, entries.length) }, async () => {
      for (;;) {
        const index = next++
        if (index >= entries.length) return
        await worker(entries[index])
      }
    }))
  }

  async function finalizeSession(sessionID) {
    const response = await fetch(`/upload-sessions/${sessionID}/finalize`, { method: 'POST', headers: { 'X-CSRF-Token': csrf } })
    if (!response.ok) throw new Error(await response.text())
    return response.json()
  }

  function joinSessionFiles(session, localFiles) {
    const localByPath = new Map(localFiles.map((item) => [item.path, item.file]))
    return session.files.map((entry) => {
      const file = localByPath.get(entry.relative_path)
      if (!file || file.size !== entry.size_bytes) throw new Error(`Folder does not match session manifest: ${entry.relative_path}`)
      return { ...entry, file }
    })
  }

  function progressRow(entry) {
    const row = document.createElement('div')
    row.className = 'upload-entry'
    const label = document.createElement('span')
    label.className = 'mono upload-name'
    label.textContent = entry.relative_path
    const meter = document.createElement('progress')
    meter.max = entry.size_bytes || 1
    meter.value = entry.status === 'COMPLETE' ? meter.max : 0
    const state = document.createElement('span')
    state.textContent = entry.status === 'COMPLETE' ? 'Complete' : 'Pending'
    row.append(label, meter, state)
    progress.append(row)
    return { row, meter, state }
  }

  async function runUpload(localFiles, existingSessionID = '') {
    progress.replaceChildren()
    status.textContent = existingSessionID ? 'Matching selected folder to saved session…' : 'Creating upload session…'
    const session = existingSessionID ? await getSession(existingSessionID) : await createSession(localFiles)
    const entries = joinSessionFiles(session, localFiles)
    const rows = new Map(entries.map((entry) => [entry.id, progressRow(entry)]))
    const pending = entries.filter((entry) => entry.status !== 'COMPLETE')
    let failures = 0
    await runPool(pending, concurrency, async (entry) => {
      const item = rows.get(entry.id)
      item.state.textContent = 'Uploading'
      try {
        await uploadEntry(session.id, entry, (loaded) => { item.meter.value = loaded })
        item.meter.value = item.meter.max
        item.state.textContent = 'Complete'
      } catch (error) {
        failures++
        item.state.textContent = 'Failed — server keeps a .partial file'
        const retry = document.createElement('button')
        retry.type = 'button'
        retry.textContent = 'Retry'
        retry.addEventListener('click', async () => {
          retry.disabled = true
          try {
            await uploadEntry(session.id, entry, (loaded) => { item.meter.value = loaded })
            item.meter.value = item.meter.max
            item.state.textContent = 'Complete'
            retry.remove()
            failures--
            if (failures === 0) await finish(session.id)
          } catch (retryError) {
            item.state.textContent = 'Retry failed'
            retry.disabled = false
          }
        })
        item.row.append(retry)
      }
    })
    if (failures === 0) await finish(session.id)
    else status.textContent = 'Some files failed. Press Retry for each failed file.'
  }

  async function finish(sessionID) {
    status.textContent = 'Finalizing folder…'
    await finalizeSession(sessionID)
    status.textContent = 'Upload complete. Opening submission…'
    window.location.reload()
  }

  async function begin(event) {
    try {
      const files = await collectFiles(event)
      if (files.length === 0) throw new Error('No files selected')
      const selectedResume = resumeSessionID
      resumeSessionID = ''
      await runUpload(files, selectedResume)
    } catch (error) {
      status.textContent = error.message || 'Upload failed'
    } finally {
      picker.value = ''
    }
  }

  selectButton.addEventListener('click', () => { resumeSessionID = ''; picker.click() })
  picker.addEventListener('change', begin)
  dropZone.addEventListener('dragover', (event) => { event.preventDefault(); dropZone.classList.add('dragging') })
  dropZone.addEventListener('dragleave', () => dropZone.classList.remove('dragging'))
  dropZone.addEventListener('drop', (event) => { event.preventDefault(); dropZone.classList.remove('dragging'); resumeSessionID = ''; begin(event) })
  dropZone.addEventListener('keydown', (event) => {
    if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); selectButton.click() }
  })
  for (const button of root.querySelectorAll('[data-upload-resume]')) {
    button.addEventListener('click', () => { resumeSessionID = button.dataset.uploadResume; picker.click() })
  }
  for (const button of root.querySelectorAll('[data-upload-delete]')) {
    button.addEventListener('click', async () => {
      button.disabled = true
      const response = await fetch(`/upload-sessions/${button.dataset.uploadDelete}`, { method: 'DELETE', headers: { 'X-CSRF-Token': csrf } })
      if (response.ok) window.location.reload()
      else { status.textContent = await response.text(); button.disabled = false }
    })
  }
})()
