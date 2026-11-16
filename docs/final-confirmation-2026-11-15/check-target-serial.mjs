import {mkdtemp, writeFile, rm} from 'node:fs/promises'
import {tmpdir} from 'node:os'
import {join} from 'node:path'
import {fileURLToPath} from 'node:url'
import {spawnSync} from 'node:child_process'

const root = fileURLToPath(new URL('../../', import.meta.url))
const here = fileURLToPath(new URL('./', import.meta.url))
const scratch = await mkdtemp(join(tmpdir(), 'cfst-confirm-target-'))
try {
  const overlay = join(scratch, 'overlay.json')
  await writeFile(overlay, JSON.stringify({Replace: {[join(root, 'internal/management/zz_confirmation_target_test.go')]: join(here, 'target_serial_test.go.txt')}}))
  const result = spawnSync('go', ['test', '-mod=readonly', '-overlay', overlay, './internal/management', '-run', '^TestConfirmationConcurrentTargetWritesKeepActualOrder$', '-race', '-count=10', '-v'], {cwd: root, stdio: 'inherit', env: {...process.env, GOPROXY: 'off', GOSUMDB: 'off'}})
  process.exitCode = result.status ?? 1
} finally {
  await rm(scratch, {recursive: true, force: true})
}
