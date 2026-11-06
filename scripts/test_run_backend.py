import importlib.util
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('run_backend', Path(__file__).with_name('run-backend.py'))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class SettingsTests(unittest.TestCase):
    def setUp(self):
        self.previous = module.ROOT
        self.directory = tempfile.TemporaryDirectory()
        module.ROOT = Path(self.directory.name)
        self.addCleanup(self.directory.cleanup)
        self.addCleanup(setattr, module, 'ROOT', self.previous)

    def test_reads_literal_settings_without_shell_expansion(self):
        (module.ROOT / '.env').write_text('POSTGRES_USER=user\nPOSTGRES_PASSWORD=$(literal)\nPOSTGRES_DB=office\n')
        self.assertEqual(module.read_settings()['POSTGRES_PASSWORD'], '$(literal)')

    def test_missing_password_is_rejected(self):
        (module.ROOT / '.env').write_text('POSTGRES_USER=user\nPOSTGRES_DB=office\n')
        with self.assertRaises(ValueError):
            module.read_settings()

    def test_api_key_is_not_forwarded_from_dotenv(self):
        (module.ROOT / '.env').write_text('POSTGRES_USER=user\nPOSTGRES_PASSWORD=password\nPOSTGRES_DB=office\nGEMINI_API_KEY=synthetic-key\n')
        frontend = module.ROOT / 'frontend' / 'dist'
        frontend.mkdir(parents=True)
        (frontend / 'index.html').touch()
        with patch.dict(module.os.environ, {}, clear=True), patch.object(module.subprocess, 'call', return_value=0) as run:
            self.assertEqual(module.main(), 0)
        self.assertNotIn('GEMINI_API_KEY', run.call_args.kwargs['env'])
        self.assertNotIn('synthetic-key', repr(run.call_args.args))


if __name__ == '__main__':
    unittest.main()
