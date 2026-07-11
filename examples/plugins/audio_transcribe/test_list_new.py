import os
import tempfile
import unittest

from list_new import find_new_files


class TestFindNewFiles(unittest.TestCase):
    def test_skips_processed_files(self):
        with tempfile.TemporaryDirectory() as d:
            open(os.path.join(d, "a.wav"), "w").close()
            open(os.path.join(d, "b.wav"), "w").close()
            open(os.path.join(d, "b.wav.done"), "w").close()
            self.assertEqual(find_new_files(d), ["a.wav"])

    def test_ignores_non_audio_files(self):
        with tempfile.TemporaryDirectory() as d:
            open(os.path.join(d, "notes.txt"), "w").close()
            open(os.path.join(d, "c.mp3"), "w").close()
            self.assertEqual(find_new_files(d), ["c.mp3"])

    def test_empty_dir_returns_empty_list(self):
        with tempfile.TemporaryDirectory() as d:
            self.assertEqual(find_new_files(d), [])

    def test_sorted_order(self):
        with tempfile.TemporaryDirectory() as d:
            open(os.path.join(d, "z.wav"), "w").close()
            open(os.path.join(d, "a.wav"), "w").close()
            self.assertEqual(find_new_files(d), ["a.wav", "z.wav"])


if __name__ == "__main__":
    unittest.main()
