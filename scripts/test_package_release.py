import tempfile
import unittest
from pathlib import Path

from package_release import collect_release_files, write_release


class ReleasePackageTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        for name in ('control-plane', 'migrate', 'admin-bootstrap', 'node-bootstrap', 'agent', 'agent-token'):
            target = self.root / 'bin' / name
            target.parent.mkdir(parents=True, exist_ok=True)
            image = bytearray(20)
            image[:4] = b'\x7fELF'
            image[4] = 2
            image[5] = 1
            image[18:20] = (62).to_bytes(2, 'little')
            target.write_bytes(image)
        self.put('README.md', 'deployment overview')
        self.put('docs/deployment/vps-webui.md', 'formal HTTP and HTTPS steps')
        self.put('apps/web/dist/index.html', '<html></html>')
        self.put('apps/web/dist/assets/app.js', 'console.log(1)')
        self.put('migrations/000001_init.up.sql', 'SELECT 1;')
        self.put('migrations/000001_init.down.sql', 'SELECT 1;')
        for name in ('compose.yaml', 'compose.agent-local.yaml', 'compose.agent-tls.yaml',
                     'compose.proxy-secrets.yaml', 'compose.relay-secrets.yaml',
                     'Dockerfile.prebuilt', 'Dockerfile.agent', '.env.example'):
            self.put('deployments/compose/' + name, 'fixture')

    def put(self, name, content):
        target = self.root / name
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(content)

    def test_release_contains_runtime_only_and_no_secrets_or_appledouble(self):
        self.put('deployments/compose/.env', 'POSTGRES_PASSWORD=secret')
        self.put('migrations/._000001_init.up.sql', 'mac metadata')
        self.put('apps/web/dist/assets/._app.js', 'mac metadata')
        self.put('.git/config', 'private')
        output = self.root / 'release.tar.gz'
        write_release(self.root, output)
        import tarfile
        with tarfile.open(output) as archive:
            names = archive.getnames()
            self.assertIn('bin/control-plane', names)
            self.assertIn('README.md', names)
            self.assertIn('docs/deployment/vps-webui.md', names)
            self.assertIn('apps/web/dist/assets/app.js', names)
            self.assertIn('migrations/000001_init.up.sql', names)
            self.assertNotIn('deployments/compose/.env', names)
            self.assertFalse(any('/._' in name or name.startswith('.git/') for name in names))

    def test_rejects_missing_migration_pair_or_wrong_binary_architecture(self):
        (self.root / 'migrations/000001_init.down.sql').unlink()
        with self.assertRaisesRegex(ValueError, 'migration'):
            collect_release_files(self.root)
        self.put('migrations/000001_init.down.sql', 'SELECT 1;')
        (self.root / 'bin/agent').write_bytes(b'not a Linux binary')
        with self.assertRaisesRegex(ValueError, 'Linux amd64'):
            collect_release_files(self.root)

    def test_rejects_incomplete_web_or_compose_and_symlinked_artifacts(self):
        (self.root / 'apps/web/dist/index.html').unlink()
        with self.assertRaisesRegex(ValueError, 'WebUI'):
            collect_release_files(self.root)
        self.put('apps/web/dist/index.html', '<html></html>')
        (self.root / 'deployments/compose/compose.yaml').unlink()
        with self.assertRaisesRegex(ValueError, 'Compose'):
            collect_release_files(self.root)
        self.put('deployments/compose/compose.yaml', 'fixture')
        (self.root / 'bin/agent').unlink()
        (self.root / 'bin/agent').symlink_to(self.root / 'bin/control-plane')
        with self.assertRaisesRegex(ValueError, 'symlink'):
            write_release(self.root, self.root / 'release.tar.gz')


if __name__ == '__main__':
    unittest.main()
