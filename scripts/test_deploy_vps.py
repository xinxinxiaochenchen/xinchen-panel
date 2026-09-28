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
        (self.root / 'deployments/compose/compose.proxy-secrets.yaml').write_text('services: {}\n')
        (self.root / 'scripts').mkdir()
        (self.root / 'scripts/deploy-vps.sh').write_bytes(SCRIPT.read_bytes())
        bindir = self.root / 'fake-bin'
        bindir.mkdir()
        docker = bindir / 'docker'
        docker.write_text('#!/bin/sh\nprintf "%s\\n" "$*" >> "$DOCKER_LOG"\ncase " $* " in\n  *" volume inspect "*) [ "${EXISTING_VOLUME:-0}" = 1 ] ;;\n  *" exec -T db pg_dump "*) [ "${FAIL_BACKUP:-0}" = 1 ] && exit 1; printf "fixture-backup" ;;\n  *" exec -T db pg_restore "*) printf "fixture-index" ;;\n  *" build migrate api "*) [ "${FAIL_BUILD:-0}" != 1 ] ;;\n  *" run --rm -T --no-deps migrate "*) [ "${FAIL_MIGRATION:-0}" != 1 ] ;;\n  *"admin-bootstrap --status "*) printf \'%s\\n\' "${ADMIN_STATE:-empty}" ;;\n  *"admin-bootstrap --if-needed "*) [ "${FAIL_BOOTSTRAP:-0}" = 1 ] && exit 1; cat > "$BOOTSTRAP_INPUT" ;;\nesac\n')
        docker.chmod(0o755)
        self.env = os.environ.copy()
        self.env['PATH'] = str(bindir) + os.pathsep + self.env['PATH']
        self.env['DOCKER_LOG'] = str(self.root / 'docker.log')
        self.env['NCP_DEPLOY_ROOT'] = str(self.root)
        password_file = self.root / 'admin-password'
        password_file.write_text('long-initial-password\n')
        password_file.chmod(0o600)
        self.env['CONTROL_ADMIN_EMAIL'] = 'owner@example.test'
        self.env['CONTROL_ADMIN_PASSWORD_FILE'] = str(password_file)
        self.env['BOOTSTRAP_INPUT'] = str(self.root / 'bootstrap-input')

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
        self.assertIn('CONTROL_BIND_IP=0.0.0.0', envfile.read_text())
        self.assertIn('CONTROL_BROWSER_AUTH_ENABLED=true', envfile.read_text())
        self.assertIn('CONTROL_BROWSER_COOKIE_SECURE=false', envfile.read_text())
        password = next(line.split('=', 1)[1] for line in envfile.read_text().splitlines() if line.startswith('POSTGRES_PASSWORD='))
        self.assertRegex(password, r'^[0-9a-f]{64}$')
        calls = self.calls()
        self.assertTrue(any('compose.source.yaml' in call and 'build migrate api' in call for call in calls), calls)
        db = next(index for index, call in enumerate(calls) if 'up -d --wait db' in call)
        migration = next(index for index, call in enumerate(calls) if 'run --rm -T --no-deps migrate' in call)
        start = next(index for index, call in enumerate(calls) if 'up -d --no-deps api' in call)
        self.assertLess(db, migration)
        self.assertLess(migration, start)
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
        upgrade = next(index for index, call in enumerate(calls) if 'run --rm -T --no-deps migrate' in call)
        self.assertLess(dump, upgrade)
        self.assertEqual(len(list((self.root / '.local/backups').glob('*.dump.*'))), 1)

    def test_failed_backup_stops_before_migration(self):
        self.env['EXISTING_VOLUME'] = '1'
        self.env['FAIL_BACKUP'] = '1'
        (self.root / 'deployments/compose/.env').write_text('POSTGRES_PASSWORD=existing-password\nCONTROL_BIND_IP=127.0.0.1\nCONTROL_BROWSER_AUTH_ENABLED=false\n')
        result = self.run_script()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('backup failed', result.stderr.lower())
        self.assertFalse(any('run --rm' in call or 'up -d --no-deps api' in call for call in self.calls()))

    def test_failed_migration_stops_before_api_restart(self):
        self.env['FAIL_MIGRATION'] = '1'
        result = self.run_script()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('migration failed', result.stderr.lower())
        self.assertFalse(any('up -d --no-deps api' in call for call in self.calls()))

    def test_https_mode_enables_login_with_secure_cookies_and_loopback(self):
        result = self.run_script('--https')
        self.assertEqual(result.returncode, 0, result.stderr)
        config = (self.root / 'deployments/compose/.env').read_text()
        self.assertIn('CONTROL_BIND_IP=127.0.0.1', config)
        self.assertIn('CONTROL_BROWSER_AUTH_ENABLED=true', config)
        self.assertIn('CONTROL_BROWSER_COOKIE_SECURE=true', config)

    def test_formal_install_creates_private_key_and_bootstraps_before_start(self):
        result = self.run_script('--public-http')
        self.assertEqual(result.returncode, 0, result.stderr)
        key = self.root / '.local/secrets/proxy/proxy.key'
        self.assertEqual(stat.S_IMODE(key.stat().st_mode), 0o600)
        import base64
        self.assertEqual(len(base64.urlsafe_b64decode(key.read_text().strip() + '=')), 32)
        self.assertEqual((self.root / 'bootstrap-input').read_text(), 'long-initial-password\n')
        self.assertNotIn('long-initial-password', result.stdout + result.stderr + '\n'.join(self.calls()))
        self.assertTrue(any('compose.proxy-secrets.yaml' in call for call in self.calls()))
        bootstrap = next(i for i, call in enumerate(self.calls()) if 'admin-bootstrap --if-needed' in call)
        start = next(i for i, call in enumerate(self.calls()) if 'up -d --no-deps api' in call)
        self.assertLess(bootstrap, start)

    def test_repeated_formal_update_keeps_key_password_and_does_not_bootstrap_again(self):
        first = self.run_script('--public-http')
        self.assertEqual(first.returncode, 0, first.stderr)
        key = self.root / '.local/secrets/proxy/proxy.key'
        original_key = key.read_bytes()
        envfile = self.root / 'deployments/compose/.env'
        original_config = envfile.read_text()
        self.env['EXISTING_VOLUME'] = '1'
        self.env['ADMIN_STATE'] = 'configured'
        self.log_reset()
        result = self.run_script()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(key.read_bytes(), original_key)
        self.assertEqual(envfile.read_text(), original_config)
        self.assertFalse(any('admin-bootstrap --if-needed' in call for call in self.calls()))

    def log_reset(self):
        (self.root / 'docker.log').unlink(missing_ok=True)

    def test_existing_preview_keeps_its_mode_unless_explicitly_switched(self):
        envfile = self.root / 'deployments/compose/.env'
        envfile.write_text('POSTGRES_PASSWORD=existing-password\nCONTROL_BIND_IP=0.0.0.0\nCONTROL_BROWSER_AUTH_ENABLED=false\n')
        result = self.run_script()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('CONTROL_BROWSER_AUTH_ENABLED=false', envfile.read_text())
        self.assertFalse(any('admin-bootstrap' in call for call in self.calls()))
        result = self.run_script('--public-http')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('POSTGRES_PASSWORD=existing-password', envfile.read_text())
        self.assertIn('CONTROL_BROWSER_AUTH_ENABLED=true', envfile.read_text())

    def test_failed_bootstrap_stops_api_start_and_can_be_retried(self):
        self.env['FAIL_BOOTSTRAP'] = '1'
        result = self.run_script('--public-http')
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(any('up -d --no-deps api' in call for call in self.calls()))
        key = (self.root / '.local/secrets/proxy/proxy.key').read_bytes()
        self.env['FAIL_BOOTSTRAP'] = '0'
        result = self.run_script()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual((self.root / '.local/secrets/proxy/proxy.key').read_bytes(), key)

    def test_existing_authenticated_database_missing_key_is_not_rekeyed(self):
        self.env['EXISTING_VOLUME'] = '1'
        (self.root / 'deployments/compose/.env').write_text('POSTGRES_PASSWORD=existing-password\nCONTROL_BIND_IP=127.0.0.1\nCONTROL_BROWSER_AUTH_ENABLED=true\n')
        result = self.run_script()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('key', result.stderr.lower())
        self.assertFalse((self.root / '.local/secrets/proxy/proxy.key').exists())

    def test_public_preview_does_not_create_administrator_or_key(self):
        result = self.run_script('--public-preview')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse((self.root / '.local/secrets/proxy/proxy.key').exists())
        self.assertFalse(any('admin-bootstrap' in call for call in self.calls()))

    def test_http_https_switch_keeps_the_same_key(self):
        first = self.run_script('--public-http')
        self.assertEqual(first.returncode, 0, first.stderr)
        key = (self.root / '.local/secrets/proxy/proxy.key').read_bytes()
        self.env['ADMIN_STATE'] = 'configured'
        result = self.run_script('--https')
        self.assertEqual(result.returncode, 0, result.stderr)
        config = (self.root / 'deployments/compose/.env').read_text()
        self.assertIn('CONTROL_BROWSER_COOKIE_SECURE=true', config)
        self.assertIn('CONTROL_BIND_IP=127.0.0.1', config)
        self.assertEqual((self.root / '.local/secrets/proxy/proxy.key').read_bytes(), key)

    def test_upgrade_accepts_legacy_boolean_forms(self):
        first = self.run_script('--public-http')
        self.assertEqual(first.returncode, 0, first.stderr)
        envfile = self.root / 'deployments/compose/.env'
        envfile.write_text(envfile.read_text().replace('CONTROL_BROWSER_AUTH_ENABLED=true', 'CONTROL_BROWSER_AUTH_ENABLED=1').replace('CONTROL_BROWSER_COOKIE_SECURE=false', 'CONTROL_BROWSER_COOKIE_SECURE=0'))
        self.env['ADMIN_STATE'] = 'configured'
        result = self.run_script()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('CONTROL_DEPLOYMENT_MODE=http', envfile.read_text())

    def test_update_preserves_persisted_agent_and_relay_overlays(self):
        first = self.run_script('--public-http')
        self.assertEqual(first.returncode, 0, first.stderr)
        envfile = self.root / 'deployments/compose/.env'
        with envfile.open('a') as config:
            config.write('CONTROL_AGENT_TLS_DIR=/private/agent-tls\nCONTROL_RELAY_SECRET_DIR=/private/relay\n')
        self.env['ADMIN_STATE'] = 'configured'
        self.log_reset()
        result = self.run_script()
        self.assertEqual(result.returncode, 0, result.stderr)
        start = next(call for call in self.calls() if 'up -d --no-deps api' in call)
        self.assertIn('compose.agent-tls.yaml', start)
        self.assertIn('compose.relay-secrets.yaml', start)
        self.assertIn('compose.proxy-secrets.yaml', start)
        self.assertIn('compose.source.yaml', start)

    def test_world_readable_initial_password_file_stops_bootstrap(self):
        Path(self.env['CONTROL_ADMIN_PASSWORD_FILE']).chmod(0o644)
        result = self.run_script('--public-http')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('permissions', result.stderr)
        self.assertFalse(any('admin-bootstrap --if-needed' in call or 'up -d --no-deps api' in call for call in self.calls()))

    def test_key_ownership_is_repaired_after_a_failed_first_build(self):
        self.env['FAIL_BUILD'] = '1'
        result = self.run_script('--public-http')
        self.assertNotEqual(result.returncode, 0)
        key_path = self.root / '.local/secrets/proxy/proxy.key'
        original = key_path.read_bytes()
        self.env['FAIL_BUILD'] = '0'
        self.log_reset()
        result = self.run_script()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(key_path.read_bytes(), original)
        self.assertTrue(any('--entrypoint chown' in call and '65532:65532' in call for call in self.calls()), self.calls())

    def test_preview_switch_does_not_allow_rekeying_existing_formal_database(self):
        result = self.run_script('--public-http')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.env['EXISTING_VOLUME'] = '1'
        result = self.run_script('--public-preview')
        self.assertEqual(result.returncode, 0, result.stderr)
        key_path = self.root / '.local/secrets/proxy/proxy.key'
        key_path.unlink()
        self.log_reset()
        result = self.run_script('--public-http')
        self.assertNotEqual(result.returncode, 0, 'lost credentials must never be silently replaced')
        self.assertIn('Restore the original key', result.stderr)
        self.assertFalse(key_path.exists())
        self.assertFalse(any('run --rm' in call or 'up -d --no-deps api' in call for call in self.calls()))


if __name__ == '__main__':
    unittest.main()
