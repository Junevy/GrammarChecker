/* ============================================================
 * GrammarChecker · API 请求层
 * 契约唯一依据：api/README.md（字段名与后端 JSON tag 严格一致）。
 * 部署形态：后端 go:embed 同源托管本前端，默认走相对路径 /api/**；
 * 仅本地开发直接以 file:// 打开时需要自行启动后端（fetch 会失败并弹提示）。
 * ============================================================ */

const API = {
  /* 统一请求：非 2xx 时抛出 {code, message}；204 返回 null */
  async request(method, path, body) {
    let resp;
    try {
      resp = await fetch(path, {
        method,
        headers: body !== undefined ? { 'Content-Type': 'application/json; charset=utf-8' } : undefined,
        body: body !== undefined ? JSON.stringify(body) : undefined
      });
    } catch (e) {
      /* 网络层失败：后端未启动 / file:// 直开 */
      throw { code: 'network_error', message: '无法连接后端服务（127.0.0.1:8899），请先启动 GrammarChecker 后端' };
    }
    if (resp.status === 204) return null;
    let data = null;
    let parseFailed = false;
    try { data = await resp.json(); } catch (e) { parseFailed = true; }
    if (resp.ok && parseFailed) {
      /* 2xx 但响应体不是合法 JSON（如后端 bug 导致重复写响应体）：
         显式抛错，避免调用方拿到 null 后报出难懂的 TypeError */
      throw { code: 'bad_response', message: '后端响应不是合法 JSON，请检查后端版本/日志' };
    }
    if (!resp.ok) {
      throw {
        code: (data && data.code) || 'internal_error',
        message: (data && data.message) || ('请求失败（HTTP ' + resp.status + '）')
      };
    }
    return data;
  },
  get(path)          { return this.request('GET', path); },
  post(path, body)   { return this.request('POST', path, body); },
  put(path, body)    { return this.request('PUT', path, body); },
  del(path)          { return this.request('DELETE', path); },

  /* ---- 业务接口封装（与 api/README.md 章节一一对应） ---- */

  /* §1.1 语法检查 [已实现占位] */
  check(text)                     { return this.post('/api/check', { text }); },
  /* §1.2/1.3 检查历史 */
  checkHistory(limit)             { return this.get('/api/history/check?limit=' + (limit || 20)); },
  clearCheckHistory()             { return this.del('/api/history/check'); },
  /* §1.4 加入错词本 */
  addWrongWord(payload)           { return this.post('/api/wrong-words', payload); },

  /* §2 仓库 */
  wrongWords()                    { return this.get('/api/wrong-words'); },
  deleteWrongWord(id)             { return this.del('/api/wrong-words/' + id); },
  sentences()                     { return this.get('/api/sentences'); },
  /* §2.4/2.5 例句添加与删除（检查页「收藏例句」复用，携带结构分析缓存） */
  addSentence(english, chinese, analysisJson, source) {
    return this.post('/api/sentences', { english, chinese, analysis_json: analysisJson || '', source: source || 'manual' });
  },
  deleteSentence(id)              { return this.del('/api/sentences/' + id); },
  discriminations()               { return this.get('/api/discriminations'); },
  /* §2.7 删除单条辨析记录（仓库·辨析选项卡编辑模式） */
  deleteDiscrimination(id)        { return this.del('/api/discriminations/' + id); },

  /* §3.1 辨析 [已实现占位 + 历史复用短路] */
  discriminate(word, reuse)       { return this.post('/api/discriminate', { word, reuse_history: !!reuse }); },
  clearDiscHistory()              { return this.del('/api/discrimination-history'); },
  /* §3.3 / §4.4 收藏（幂等） */
  favDiscrimination(id, on)       { return this.request(on ? 'POST' : 'DELETE', '/api/discriminations/' + id + '/favorite'); },
  favExpression(id, on)           { return this.request(on ? 'POST' : 'DELETE', '/api/expression-history/' + id + '/favorite'); },

  /* §4 表达 */
  express(text)                   { return this.post('/api/express', { text }); },
  expressionHistory(limit)        { return this.get('/api/expression-history?limit=' + (limit || 20)); },
  clearExpHistory()               { return this.del('/api/expression-history'); },

  /* §5.1 趋势统计 */
  trends(range, type)               { return this.get('/api/trends?range=' + (range || 14) + (type ? '&type=' + encodeURIComponent(type) : '')); },

  /* §6 配置 */
  settings()                      { return this.get('/api/settings'); },
  saveSettings(kv)                { return this.put('/api/settings', kv); },

  /* §7.1 成分说明（role 精确匹配，需 URL 编码） */
  compInfo(role)                  { return this.get('/api/comp-info?role=' + encodeURIComponent(role)); },
  /* §7.2 LLM 用法示例 [已实现占位] */
  usageExample(role, sentence)    { return this.post('/api/llm/usage-example', { role, sentence }); }
};
