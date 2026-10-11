import unittest
import build
class Links(unittest.TestCase):
 def test_nested_raw_links_keep_markdown_and_code(self):
  text='[Guide](../id-capture.md#http-contract)\n\n```text\n[x](missing.md)\n```\n`[x](missing.md)`'
  out=build.raw_markdown(text,'docs/agents/index.md','en',{'docs/id-capture.md':''})
  self.assertIn('/raw/en/main/docs/id-capture.md#http-contract',out)
  self.assertIn('```text\n[x](missing.md)\n```',out)
  self.assertIn('`[x](missing.md)`',out)
 def test_cross_repository_agents_remain_raw(self):
  out=build.raw_markdown('[x](https://github.com/a/b/blob/main/docs/index.md)','README.md','en',{})
  self.assertIn('https://raw.githubusercontent.com/a/b/main/docs/index.md',out)
 def test_unknown_local_target_fails(self):
  with self.assertRaises(ValueError):build.resolve('README.md','missing.md','en',{})
 def test_spanish_routes_preserve_scope(self):
  self.assertIn('/es/main/docs/a.html',build.resolve('README.md','docs/a.md','es',{'docs/a.md':''}))
if __name__=='__main__':unittest.main()
