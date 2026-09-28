import os
import stat
import subprocess
import tempfile
import unittest
from pathlib import Path


SCRIPT = Path(__file__).with_name('deploy-vps.sh')


class DeployVPSTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        (self.root / 'deployments/compose').mkdir(parents=True)
        (self.root / 'deployments/compose/compose.yaml').write_text('services: {}\n')
        (self.root / 'deployments/compose/compose.source.yaml').write_text('services: {}\n')
        (self.root / 'scripts').mkdir()
        (self.root / 'scripts/deploy-vps.sh').write_bytes(SCRIPT.read_bytes())
        bindir = self.root / 'fake-bin'
        bindir.mkdir()
        docker = bindir / 'docker'
        docker.write_text('#!/bin/sh\nprintf "%s\\n" "$*" >> "$DOCKER_LOG"\ncase " $* " in\n  *" volume inspect "*) [ "${EXISTING_VOLUME:-0}" = 1 ] ;;\n  *" exec -T db pg_dump "*) [ "${FAIL_BACKUP:-0}" = 1 ] && exit 1; printf "fixture-backup" ;;\n  *" exec -T db pg_restore "*) printf "fixture-index" ;;\nesac\n')
        docker.chmod(0o755)
        self.env = os.environ.copy()
        self.env['PATH'] = str(bindir) + os.pathsep + self.env['PATH']
        self.env['DOCKER_LOG'] = str(self.root / 'docker.log')
        self.env['NCP_DEPLOY_ROOT'] = str(self.root)

    def run_script(self, *args):
        return subprocess.run(['sh', str(self.root / 'scripts/deploy-vps.sh'), *args], cwd=self.root, env=self.env, text=True, capture_output=True)

    def calls(self):
        path = self.root / 'docker.log'
        return path.read_text().splitlines() if path.exists() else []

    def test_fresh_deploy_creates_private_env_and_starts_source_images(self):
        result = self.run_script()
        self.assertEqual(result.returncode, 0, result.stderr)
        envfile = self.root / 'deployments/compose/.env'
        self.assertEqual(stat.S_IMODE(envfile.stat().st_mode), 0o600)
        self.assertIn('CONTROL_BIND_IP=127.0.0.1', envfile.read_text())
        self.assertIn('CONTROL_BROWSER_AUTH_ENABLED=false', envfile.read_text())
        password = next(line.split('=', 1)[1] for line in envfile.read_text().splitlines() if line.startswith('POSTGRES_PASSWORD='))
        self.assertRegex(password, r'^[0-9a-f]{64}$')
        calls = self.calls()
        self.assertTrue(any('compose.source.yaml' in call and 'up -d --build db migrate api' in call for call in calls), calls)
        self.assertTrue(any('exec -T api /usr/local/bin/control-plane --healthcheck' in call for call in calls), calls)

    def test_public_preview_requires_explicit_flag(self):
        result = self.run_script('--public-preview')
        self.assertEqual(result.returncode, 0, result.stderr)
        envfile = self.root / 'deployments/compose/.env'
        self.assertIn('CONTROL_BIND_IP=0.0.0.0', envfile.read_text())
        self.assertIn('CONTROL_BROWSER_AUTH_ENABLED=false', envfile.read_text())

    def test_existing_database_without_env_is_rejected(self):
        self.env['EXISTING_VOLUME'] = '1'
        result = self.run_script()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('existing database', result.stderr.lower())
        self.assertFalse((self.root / 'deployments/compose/.env').exists())

    def test_existing_database_is_backed_up_before_migration(self):
        self.env['EXISTING_VOLUME'] = '1'
        (self.root / 'deployments/compose/.env').write_text('POSTGRES_PASSWORD=existing-password\nCONTROL_BIND_IP=127.0.0.1\nCONTROL_BROWSER_AUTH_ENABLED=false\n')
        result = self.run_script()
        self.assertEqual(result.returncode, 0, result.stderr)
        calls = self.calls()
        dump = next(index for index, call in enumerate(calls) if 'exec -T db pg_dump' in call)
        upgrade = next(index for index, call in enumerate(calls) if 'up -d --build db migrate api' in call)
        self.assertLess(dump, upgrade)
        self.assertEqual(len(list((self.root / '.local/backups').glob('*.dump'))), 1)

    def test_failed_backup_stops_before_migration(self):
        self.env['EXISTING_VOLUME'] = '1'
        self.env['FAIL_BACKUP'] = '1'
        (self.root / 'deployments/compose/.env').write_text('POSTGRES_PASSWORD=existing-password\nCONTROL_BIND_IP=127.0.0.1\nCONTROL_BROWSER_AUTH_ENABLED=false\n')
        result = self.run_script()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('backup failed', result.stderr.lower())
        self.assertFalse(any('up -d --build db migrate api' in call for call in self.calls()))

    def test_existing_public_or_authenticated_env_requires_explicit_safe_handling(self):
        envfile = self.root / 'deployments/compose/.env'
        envfile.write_text('POSTGRES_PASSWORD=existing-password\nCONTROL_BIND_IP=0.0.0.0\nCONTROL_BROWSER_AUTH_ENABLED=false\n')
        result = self.run_script()
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(any('up -d --build' in call for call in self.calls()))
        envfile.write_text('POSTGRES_PASSWORD=existing-password\nCONTROL_BIND_IP=127.0.0.1\nCONTROL_BROWSER_AUTH_ENABLED=true # enabled\n')
        result = self.run_script()
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(any('up -d --build' in call for call in self.calls()))


if __name__ == '__main__':
    unittest.main()
