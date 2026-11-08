const fs = require('node:fs')
const path = require('node:path')

const replacements = [
  {
    name: 'backend',
    marker: '__BULLSEYE_BACKEND_PLACEHOLDER__',
    value: process.env.NEXT_PUBLIC_BACKEND || 'simulator',
  },
  {
    name: 'hub WebSocket URL',
    marker: '__BULLSEYE_HUB_WS_URL_PLACEHOLDER__',
    value: process.env.HUB_WS_URL || 'ws://localhost:3030/',
  },
]

const backend = replacements[0].value
if (backend !== 'hub' && backend !== 'simulator') {
  throw new Error(`Invalid NEXT_PUBLIC_BACKEND value: ${backend}`)
}

const hubURL = new URL(replacements[1].value)
if (hubURL.protocol !== 'ws:' && hubURL.protocol !== 'wss:') {
  throw new Error('HUB_WS_URL must use the ws:// or wss:// protocol')
}
replacements[1].value = hubURL.toString()

const staticDirectory = '/app/.next/static'
const appliedConfigPath = '/app/.dashboard-runtime-config.json'
const counts = new Map(replacements.map(({ marker }) => [marker, 0]))
const changedFiles = []

function replaceInDirectory(directory) {
  for (const entry of fs.readdirSync(directory, { withFileTypes: true })) {
    const filePath = path.join(directory, entry.name)
    if (entry.isDirectory()) {
      replaceInDirectory(filePath)
      continue
    }
    if (!entry.isFile() || !entry.name.endsWith('.js')) continue

    let contents = fs.readFileSync(filePath, 'utf8')
    const originalContents = contents
    for (const { marker, value } of replacements) {
      const escapedValue = JSON.stringify(value).slice(1, -1)
      const occurrences = contents.split(marker).length - 1
      if (occurrences > 0) {
        contents = contents.split(marker).join(escapedValue)
        counts.set(marker, counts.get(marker) + occurrences)
      }
    }
    if (contents !== originalContents) changedFiles.push([filePath, contents])
  }
}

replaceInDirectory(staticDirectory)

const config = {
  backend,
  hubURL: hubURL.toString(),
}
const replacedMarkers = [...counts.values()]
if (replacedMarkers.every((count) => count === 0)) {
  const appliedConfig = JSON.parse(fs.readFileSync(appliedConfigPath, 'utf8'))
  if (appliedConfig.backend !== config.backend || appliedConfig.hubURL !== config.hubURL) {
    throw new Error('Dashboard bundle was already configured with different runtime values')
  }
  console.log('Dashboard runtime config already applied')
} else {
  for (const { name, marker } of replacements) {
    if (counts.get(marker) === 0) {
      throw new Error(`No ${name} placeholder found in ${staticDirectory}`)
    }
  }

  for (const [filePath, contents] of changedFiles) {
    fs.writeFileSync(filePath, contents)
  }
  fs.writeFileSync(appliedConfigPath, JSON.stringify(config))
  console.log(`Dashboard runtime config applied: backend=${backend}, HUB_WS_URL=${hubURL}`)
}
