import os
import tempfile
import unittest

from process_file import process


def fake_transcribe(path):
    return "hello world this is a test", "en"


def fake_summarize(text):
    return "A short test recording."


class TestProcess(unittest.TestCase):
    def test_writes_marker_and_returns_result(self):
        with tempfile.TemporaryDirectory() as d:
            audio_path = os.path.join(d, "a.wav")
            open(audio_path, "w").close()

            result = process(
                "a.wav", d,
                transcribe_fn=fake_transcribe,
                summarize_fn=fake_summarize,
            )

            self.assertEqual(result["filename"], "a.wav")
            self.assertEqual(result["language"], "en")
            self.assertEqual(result["transcript"], "hello world this is a test")
            self.assertEqual(result["summary"], "A short test recording.")
            self.assertTrue(os.path.exists(audio_path + ".done"))

    def test_does_not_write_marker_if_summarize_fails(self):
        with tempfile.TemporaryDirectory() as d:
            audio_path = os.path.join(d, "b.wav")
            open(audio_path, "w").close()

            def failing_summarize(text):
                raise RuntimeError("openai call failed")

            with self.assertRaises(RuntimeError):
                process(
                    "b.wav", d,
                    transcribe_fn=fake_transcribe,
                    summarize_fn=failing_summarize,
                )
            self.assertFalse(os.path.exists(audio_path + ".done"))


if __name__ == "__main__":
    unittest.main()
