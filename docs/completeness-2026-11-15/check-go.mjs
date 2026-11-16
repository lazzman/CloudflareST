import {mkdtemp, writeFile, rm} from 'node:fs/promises'
import {tmpdir} from 'node:os'
import {join} from 'node:path'
import {fileURLToPath} from 'node:url'
import {spawnSync} from 'node:child_process'

const root = fileURLToPath(new URL('../../', import.meta.url))
const here = fileURLToPath(new URL('./', import.meta.url))
const scratch = await mkdtemp(join(tmpdir(), 'cfst-completeness-overlay-'))
try {
  const overlay = join(scratch, 'overlay.json')
  await writeFile(overlay, JSON.stringify({Replace: {
    [join(root, 'internal/management/zz_completeness_test.go')]: join(here, 'management_contracts_test.go.txt'),
  }}))
  const result = spawnSync('go', ['test', '-mod=readonly', '-overlay', overlay, './internal/management', '-run', '^TestCompleteness', '-count=1', '-vet=off', '-v'], {
    cwd: root, stdio: 'inherit', env: {...process.env, GOPROXY: 'off', GOSUMDB: 'off', GOTOOLCHAIN: 'local'},
  })
  process.exitCode = result.status ?? 1
} finally {
  await rm(scratch, {recursive: true, force: true})
}
