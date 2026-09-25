/* ============================================================
 * GrammarChecker · Vue 应用
 * 数据层：真实 API（见 js/api.js，契约见 api/README.md）。
 * 交互规范以 docs 技术文档第 7 节「通用交互规范」为验收标准。
 * ============================================================ */

const { createApp } = Vue;

/* 成分说明 404 时的本地兜底（后端 seed 未收录的角色） */
const DEFAULT_COMP_INFO = {
  what: '该成分由特定词性的词或短语充当，承担句子中的特定语法功能。',
  why: '与其他成分配合，共同构成完整、通顺的句子语义。',
  how: '依据其在句中的位置和引导词判断所属成分，再套用相应的语法规则。',
  demo: '结合例句观察该成分的位置与搭配，即可掌握其基本用法。'
};

const app = createApp({
  data() {
    return {
      /* ---- 导航 ---- */
      page: 'check',
      navList: [
        { key: 'check',        label: '检查' },
        { key: 'discriminate', label: '辨析' },
        { key: 'express',      label: '表达' },
        { key: 'repo',         label: '仓库' },
        { key: 'trend',        label: '趋势' },
        { key: 'settings',     label: '配置' }
      ],
      navStyle: { top: '0px', height: '0px' },
      navEls: {},
      tabEls: {},

      /* ---- 全局提示（错误三态：API 报错统一 toast） ---- */
      toast: { show: false, msg: '', kind: 'error', timer: null },

      /* ---- 数据层（真实 API；后端字段在 load* 中映射为视图模型） ---- */
      wrongWords: [],        // {id, word, errorType, original, corrected, pre, bad, good, post, date, analysisJson}
      sentences: [],         // {id, en, zh, src, date, analysisJson}
      discPairs: [],         // {id, word, syn, favorite, resultJson, date}
      checkHistory: [],      // {id, text, time, errs}
      discHistory: [],       // {id, word, syn, time}
      expHistory: [],        // {id, cn, en, time}
      loaded: false,         // 首屏数据是否加载完成

      /* ---- 检查页 ---- */
      charCount: 0,
      editorDraft: '',                   // 编辑器草稿（切页后恢复）
      showResult: false,
      noErrors: false,
      checking: false,
      checkResultData: null,             // POST /api/check 响应
      currentSentence: '',               // 本次检查的原文（加入错词本用）
      bubble: null,                      // {wrong, right, type} 悬停替换气泡数据
      cdRunning: false,
      cdRemain: 5,
      cdTimer: null,

      /* ---- 成分说明弹窗（内容存 DB，LLM 生成用法示例） ---- */
      compModal: { role: '', info: null, example: '', loading: false },

      /* ---- 历史弹出（检查 / 辨析 / 表达）与下拉 ---- */
      openPop: null, // 'check' | 'disc' | 'exp' | null
      ddOpen: false,
      dateOpen: false,

      /* ---- 日期筛选（仓库页） ---- */
      dateRange: '全部',
      dateRanges: [
        { label: '全部',     days: 0 },
        { label: '最近7天',  days: 7 },
        { label: '最近14天', days: 14 },
        { label: '最近30天', days: 30 }
      ],

      /* ---- 仓库页 ---- */
      repoTab: 'wrong', // wrong | sent | disc
      repoEdit: false,      // 仓库编辑模式：卡片浮出删除角标
      delConfirmKey: '',    // 待二次确认的删除目标（kind+id），4s 后自动还原
      delTimer: null,
      tabStyle: { left: '4px', width: '0px' },
      wrongSearch: '',
      sentSearch: '',
      discSearch: '',
      activeType: null,
      typeList: [],                      // 由 wrongWords 聚合：{name, n, op, gray}
      structModal: null,                 // 结构分析弹窗：{sub, struct|null, word, errorType, original, corrected}
      sceneModalData: null,              // 场景辨析弹窗：{sub, core, words}（来自 result_json）

      /* ---- 趋势页 ---- */
      trendType: '全部错误类型',
      trendTypes: ['全部错误类型'],
      trendPoints: [],                   // {date, label, x, y} 补齐空档后的画布坐标
      statToday: 0,
      statAvg: 0,
      statUp: 0,
      trendHasData: false,
      trendScale: { min: 0, max: 100 },  // 纵轴范围：随数据自适应（网格与 y 映射共用）

      /* ---- 辨析 / 表达 ---- */
      discInput: '',
      expInput: '',
      discResultData: null,              // POST /api/discriminate 响应（sub/core/words）
      expResultData: null,               // POST /api/express 响应（core/words）
      discBusy: false,
      expBusy: false,
      favDisc: false,
      favExp: false,
      favSent: false,                    // 检查页「收藏例句」双向状态
      favSentId: null,                   // 收藏后返回的例句 id（取消收藏用）

      /* ---- 配置页 ---- */
      reuseOn: true,
      keyVisible: false,
      apiKey: '',                        // GET/PUT /api/settings

      /* ---- 弹窗 ---- */
      activeModal: null // 'struct' | 'scene' | 'comp' | null
    };
  },

  computed: {
    /* 趋势 · 纵轴网格：按当前量程 4 等分（顶部 100% 位 → 底线 270） */
    trendGrid() {
      const { min, max } = this.trendScale;
      const out = [];
      for (let k = 0; k <= 4; k++) {
        const v = max - (max - min) * k / 4;
        out.push({ label: (Math.round(v * 10) / 10) + '%', y: +(20 + 250 * k / 4).toFixed(1) });
      }
      return out;
    },
    /* 日期筛选：所选范围对应的天数（0 = 不筛选） */
    rangeDays() {
      const r = this.dateRanges.find(r => r.label === this.dateRange);
      return r ? r.days : 0;
    },
    /* created_at（ISO 字符串）是否落在所选范围内（以真实当天为基准） */
    inDateRange() {
      if (!this.rangeDays) return () => true;
      const cutoff = new Date();
      cutoff.setHours(0, 0, 0, 0);
      cutoff.setDate(cutoff.getDate() - (this.rangeDays - 1));
      const min = cutoff.getTime();
      return iso => {
        const t = new Date(iso).getTime();
        return !isNaN(t) && t >= min;
      };
    },

    /* 仓库 · 先前错误筛选：类型 AND 关键词 AND 日期 */
    filteredWrong() {
      const q = this.wrongSearch.toLowerCase();
      return this.wrongWords.filter(w =>
        (!this.activeType || w.errorType === this.activeType) &&
        this.inDateRange(w.createdAt) &&
        (w.word + w.original + w.corrected).toLowerCase().includes(q)
      );
    },
    filteredSent() {
      const q = this.sentSearch.toLowerCase();
      return this.sentences.filter(s =>
        this.inDateRange(s.createdAt) &&
        (s.en + s.zh).toLowerCase().includes(q)
      );
    },
    filteredDisc() {
      const q = this.discSearch.toLowerCase();
      return this.discPairs.filter(d =>
        this.inDateRange(d.createdAt) &&
        (d.word + d.syn).toLowerCase().includes(q)
      );
    },

    /* 趋势 · 折线路径与数据点（画布坐标已在 loadTrends 中映射） */
    chartLinePoints() {
      /* 有值点之间直连（跨无记录空档），null 空档不参与连线 */
      return this.trendPoints.filter(p => p.hasValue).map(p => p.x + ',' + p.y).join(' ');
    },
    chartDots() {
      const valid = this.trendPoints.filter(p => p.hasValue);
      const last = valid.length - 1;
      return valid.map((p, i) => ({
        x: p.x, y: p.y, i, last: i === last, label: p.label
      }));
    },
    trendLabels() {
      /* x 轴标签：基于 14 天完整时间轴，最多 8 个均匀取样 */
      const pts = this.trendPoints;
      if (pts.length <= 8) return pts.map(p => ({ label: p.label, x: p.x }));
      const step = (pts.length - 1) / 7;
      const out = [];
      for (let k = 0; k < 7; k++) {
        const p = pts[Math.round(k * step)];
        out.push({ label: p.label, x: p.x });
      }
      out.push({ label: pts[pts.length - 1].label, x: pts[pts.length - 1].x });
      return out;
    },
    /* 有效数据点不足 2 个时无法连成折线，显示单点提示 */
    trendSingle() {
      return this.trendHasData && this.trendPoints.filter(p => p.hasValue).length < 2;
    },
    statDeltaText() {
      const v = this.statUp;
      return (v >= 0 ? '+' : '') + v;
    }
  },

  watch: {
    /* 液态滑块跟随导航 / 选项卡移动 */
    page() {
      this.closeAllPops();
      /* 滑块需在 DOM 更新后重新测量；仓库页滑块随页面进场初始化 */
      this.$nextTick(() => { this.updateNavGlider(); this.updateTabGlider(); this.animateTrendStats(); });
    },
    repoTab() {
      /* 切换子选项卡时退出编辑模式并清掉待确认删除 */
      this.repoEdit = false;
      this.cancelDel();
      this.$nextTick(() => this.updateTabGlider());
    },
    /* 辨析复用历史开关：即时保存到后端 */
    reuseOn(on) {
      this.persistSettings({ reuse_discrimination_history: on ? '1' : '0' });
    }
  },

  methods: {
    /* ================= 启动加载（真实 API） ================= */
    async boot() {
      /* 并行拉取首屏数据；单个失败不阻塞其余模块 */
      const tasks = [
        this.loadWrongWords(), this.loadSentences(), this.loadDiscriminations(),
        this.loadCheckHistory(), this.loadExpHistory(), this.loadSettings(), this.loadTrends()
      ];
      await Promise.allSettled(tasks);
      this.loaded = true;
    },
    async loadWrongWords() {
      try {
        const list = await API.wrongWords();
        this.wrongWords = (list || []).map(w => Object.assign(this.diffSentences(w.original_sentence, w.corrected_sentence), {
          id: w.id, word: w.word, errorType: w.error_type || '未分类',
          original: w.original_sentence, corrected: w.corrected_sentence,
          createdAt: w.created_at, date: this.fmtDate(w.created_at),
          analysisJson: w.analysis_json || ''
        }));
        this.rebuildTypeList();
      } catch (e) { this.showToast(e); }
    },
    async loadSentences() {
      try {
        const list = await API.sentences();
        this.sentences = (list || []).map(s => ({
          id: s.id, en: s.english, zh: s.chinese,
          src: s.source === 'check' ? '来自检查' : '手动添加',
          createdAt: s.created_at, date: this.fmtDate(s.created_at),
          analysisJson: s.analysis_json || ''
        }));
      } catch (e) { this.showToast(e); }
    },
    async loadDiscriminations() {
      try {
        const list = await API.discriminations();
        this.discPairs = (list || []).map(d => ({
          id: d.id, word: d.word,
          syn: this.parseSynonyms(d.synonyms_json),
          favorite: !!d.favorite,
          resultJson: d.result_json || '',
          createdAt: d.created_at, date: this.fmtDate(d.created_at)
        }));
        this.discHistory = this.discPairs.map(d => ({
          id: d.id, word: d.word, syn: d.syn, time: this.fmtTime(d.createdAt)
        }));
        /* 重建趋势页错误类型下拉 */
        const types = [...new Set(this.wrongWords.map(w => w.errorType))];
        this.trendTypes = ['全部错误类型', ...types];
      } catch (e) { this.showToast(e); }
    },
    async loadCheckHistory() {
      try {
        const list = await API.checkHistory(20);
        this.checkHistory = (list || []).map(h => ({
          id: h.id, text: h.sentence, errs: h.error_count, time: this.fmtTime(h.created_at)
        }));
      } catch (e) { this.showToast(e); }
    },
    async loadExpHistory() {
      try {
        const list = await API.expressionHistory(20);
        this.expHistory = (list || []).map(h => ({
          id: h.id, cn: h.chinese, en: '→ ' + h.recommended, time: this.fmtTime(h.created_at)
        }));
      } catch (e) { this.showToast(e); }
    },
    async loadSettings() {
      try {
        const s = await API.settings();
        this.apiKey = s.api_key || '';
        this.reuseOn = (s.reuse_discrimination_history || '1') === '1';
      } catch (e) { this.showToast(e); }
    },
    async persistSettings(kv) {
      try { await API.saveSettings(kv); }
      catch (e) { this.showToast(e); }
    },

    /* ================= 数据映射辅助 ================= */
    /* 由原句 / 修正句计算 pre + bad + good + post 片段（首个差异点） */
    diffSentences(original, corrected) {
      const o = original || '', c = corrected || '';
      if (o === c) return { pre: o, bad: '', good: '', post: '' };
      let s = 0;
      while (s < o.length && s < c.length && o[s] === c[s]) s++;
      let eo = o.length - 1, ec = c.length - 1;
      while (eo >= s && ec >= s && o[eo] === c[ec]) { eo--; ec--; }
      return { pre: o.slice(0, s), bad: o.slice(s, eo + 1), good: c.slice(s, ec + 1), post: o.slice(eo + 1) };
    },
    parseSynonyms(json) {
      try {
        const arr = JSON.parse(json || '[]');
        return Array.isArray(arr) ? arr.join(' · ') : '';
      } catch (e) { return ''; }
    },
    parseJSON(str) {
      try { const v = JSON.parse(str || ''); return v && typeof v === 'object' ? v : null; }
      catch (e) { return null; }
    },
    fmtDate(iso) {
      const d = new Date(iso);
      if (isNaN(d)) return '';
      const mm = String(d.getMonth() + 1).padStart(2, '0');
      const dd = String(d.getDate()).padStart(2, '0');
      return mm + '-' + dd;
    },
    fmtTime(iso) {
      const d = new Date(iso);
      if (isNaN(d)) return '';
      const hm = String(d.getHours()).padStart(2, '0') + ':' + String(d.getMinutes()).padStart(2, '0');
      const today = new Date(); today.setHours(0, 0, 0, 0);
      const that = new Date(d); that.setHours(0, 0, 0, 0);
      const diff = Math.round((today - that) / 86400000);
      if (diff === 0) return '今天 ' + hm;
      if (diff === 1) return '昨天 ' + hm;
      return this.fmtDate(iso) + ' ' + hm;
    },
    /* 仓库 · 类型胶囊聚合：按 error_type 计数，透明度递减 1→0.12，最后一个灰档 */
    rebuildTypeList() {
      const counts = {};
      this.wrongWords.forEach(w => { counts[w.errorType] = (counts[w.errorType] || 0) + 1; });
      const names = Object.keys(counts).sort((a, b) => counts[b] - counts[a]);
      const total = names.length;
      this.typeList = names.map((name, i) => {
        const last = i === total - 1 && total > 1;
        const op = last ? null : Math.max(0.12, +(1 - i * (0.88 / Math.max(total - 1, 1))).toFixed(2));
        return { name, n: counts[name], op, gray: total === 1 ? false : last };
      });
    },

    /* ================= 通用提示 ================= */
    showToast(e, kind) {
      const msg = typeof e === 'string' ? e : (e && e.message) || '未知错误';
      clearTimeout(this.toast.timer);
      this.toast = {
        show: true, msg, kind: kind || 'error',
        timer: setTimeout(() => { this.toast.show = false; }, 3600)
      };
    },
    /* LLM 类接口错误：无 KEY 引导去配置页 */
    showLLMError(e) {
      this.showToast(e);
      if (e && e.code === 'api_key_missing') this.page = 'settings';
    },

    /* ================= 液态滑块测量 ================= */
    /* 函数式 ref：记录导航项 / 选项卡的真实 DOM，用于测量滑块位置 */
    setNavRef(el, key) { if (el) this.navEls[key] = el; },
    setTabRef(el, key) { if (el) this.tabEls[key] = el; },
    /* 页面进场动画结束后测量（out-in 模式下此时 DOM 才真正存在） */
    onPageAfterEnter() {
      this.updateNavGlider();
      this.updateTabGlider();
      this.animateTrendStats();
      /* 回到检查页时恢复编辑器草稿（contenteditable 在 out-in 转场中被销毁重建） */
      if (this.page === 'check' && this.editorDraft) {
        const el = this.$refs.editorEl;
        if (el && !el.textContent.trim()) {
          el.textContent = this.editorDraft;
          this.charCount = Math.min(this.editorDraft.length, 500);
        }
      }
      /* 字体就绪会改变文本宽度，重测一次保证滑块贴合 */
      if (document.fonts && document.fonts.ready) {
        document.fonts.ready.then(() => { this.updateNavGlider(); this.updateTabGlider(); });
      }
    },
    updateNavGlider() {
      const el = this.navEls[this.page];
      if (el) {
        this.navStyle = { top: el.offsetTop + 'px', height: el.offsetHeight + 'px' };
      }
    },
    updateTabGlider() {
      const el = this.tabEls[this.repoTab];
      if (el) {
        this.tabStyle = { left: el.offsetLeft + 'px', width: el.offsetWidth + 'px' };
      }
    },

    /* ================= 通用弹层 ================= */
    togglePop(name) {
      this.ddOpen = false;
      this.dateOpen = false;
      this.openPop = this.openPop === name ? null : name;
    },
    closeAllPops() {
      this.openPop = null;
      this.ddOpen = false;
      this.dateOpen = false;
    },

    /* ================= 检查页 ================= */
    onEditorInput() {
      /* 统计正文字符数时排除替换气泡、对号等辅助元素 */
      const clone = this.$refs.editorEl.cloneNode(true);
      const bubble = clone.querySelector('.fix-bubble');
      if (bubble) bubble.remove();
      const mark = clone.querySelector('.ok-mark');
      if (mark) mark.remove();
      const text = clone.textContent;
      this.editorDraft = text;
      this.charCount = Math.min(text.length, 500);
    },
    focusEditor() { this.$refs.editorEl.focus(); },
    /* 悬停气泡：一键替换错误单词（数据来自检查结果） */
    replaceWrong() {
      if (!this.bubble) return;
      const el = this.$refs.editorEl;
      if (!el) return;
      const span = el.querySelector('.hl-err');
      if (span) span.replaceWith(document.createTextNode(this.bubble.right));
      this.bubble = null;
      /* 错词已改正：取消收录倒计时，并显示句尾绿色对号 */
      clearInterval(this.cdTimer);
      this.cdRunning = false;
      this.addOkMark();
      this.onEditorInput();
    },
    /* 把首个错误渲染为标红 + 悬停替换气泡（fix 格式："wrong → right"） */
    renderEditorError(err) {
      const el = this.$refs.editorEl;
      if (!el || !err || !err.fix) return;
      const parts = err.fix.split('→');
      if (parts.length < 2) return;
      const wrong = parts[0].trim(), right = parts[1].trim();
      const text = el.textContent;
      const idx = text.indexOf(wrong);
      if (idx < 0) return;
      el.textContent = text;
      const walker = document.createTreeWalker(el, NodeFilter.SHOW_TEXT);
      let node, acc = 0;
      while ((node = walker.nextNode())) {
        const start = acc, end = acc + node.textContent.length;
        if (idx < end && idx >= start) {
          const range = document.createRange();
          range.setStart(node, idx - start);
          range.setEnd(node, Math.min(idx - start + wrong.length, node.textContent.length));
          const span = document.createElement('span');
          span.className = 'hl-err';
          span.appendChild(range.extractContents());
          const bubble = document.createElement('span');
          bubble.className = 'fix-bubble';
          bubble.contentEditable = 'false';
          bubble.innerHTML = '<span class="chip red">' + (err.type || '错误') + '</span>'
            + '<span class="fb-text">替换为 <b></b></span>';
          bubble.querySelector('b').textContent = right;
          const btn = document.createElement('button');
          btn.className = 'fb-btn';
          btn.textContent = '替换';
          /* 绑定替换点击：阻止冒泡避免误触全局弹层关闭 */
          btn.addEventListener('click', (e) => {
            e.stopPropagation();
            this.replaceWrong();
          });
          bubble.appendChild(btn);
          span.appendChild(bubble);
          range.insertNode(span);
          /* 悬停管理：离开标红词后给 600ms 宽限期，鼠标路径稍偏不丢气泡 */
          let hideTimer = null;
          span.addEventListener('mouseenter', () => {
            clearTimeout(hideTimer);
            span.classList.add('bubble-open');
          });
          span.addEventListener('mouseleave', () => {
            hideTimer = setTimeout(() => span.classList.remove('bubble-open'), 600);
          });
          this.bubble = { wrong, right, type: err.type };
          this.onEditorInput();
          return;
        }
        acc = end;
      }
    },
    /* 检查页「收藏例句」：把本次检查句子（有错时按 fix 修正）存入仓库·例句（§2.4） */
    buildCorrectedSentence() {
      let s = this.currentSentence || '';
      const data = this.checkResultData;
      if (data && data.errors && data.errors.length) {
        for (const err of data.errors) {
          const parts = (err.fix || '').split('→');
          if (parts.length === 2 && parts[0].trim()) {
            const right = parts[1].trim();
            s = right ? s.replace(parts[0].trim(), right) : s;
          }
        }
      }
      return s.trim();
    },
    async toggleFavSent() {
      if (!this.showResult || !this.checkResultData) return;
      const on = !this.favSent;
      try {
        if (on) {
          const english = this.buildCorrectedSentence();
          if (!english) { this.showToast('暂无可收藏的句子', 'info'); return; }
          const struct = this.checkResultData.struct;
          const resp = await API.addSentence(
            english,
            (this.checkResultData.translation || '').trim(),
            struct ? JSON.stringify(struct) : '',
            'check'
          );
          this.favSentId = resp && resp.id;
          this.showToast('已加入仓库 · 例句', 'success');
        } else {
          if (this.favSentId) await API.deleteSentence(this.favSentId);
          this.favSentId = null;
        }
        this.favSent = on;
        this.loadSentences();
      } catch (e) { this.showToast(e); }
    },
    async doCheck() {
      this.closeAllPops();
      const text = this.$refs.editorEl ? this.$refs.editorEl.textContent.trim() : '';
      if (!text) { this.showToast('请先输入要检查的句子', 'info'); return; }
      if (this.checking) return;
      this.checking = true;
      this.showResult = false;
      this.noErrors = false;
      try {
        const resp = await API.check(text);
        this.currentSentence = text;
        this.checkResultData = resp;
        this.noErrors = !resp.errTotal || resp.errTotal === 0 || !resp.errors || resp.errors.length === 0;
        this.showResult = true;
        this.bubble = null;
        this.favSent = false;
        this.favSentId = null;
        if (this.noErrors) {
          this.addOkMark();
        } else {
          this.renderEditorError(resp.errors[0]);
          this.startCountdown();
        }
        this.loadCheckHistory();
      } catch (e) {
        this.showLLMError(e);
      } finally {
        this.checking = false;
      }
    },
    /* 句尾追加圆形绿色对号 */
    addOkMark() {
      const el = this.$refs.editorEl;
      if (!el) return;
      const old = el.querySelector('.ok-mark');
      if (old) old.remove();
      const mark = document.createElement('span');
      mark.className = 'ok-mark';
      mark.contentEditable = 'false';
      mark.innerHTML = '<svg width="11" height="9" viewBox="0 0 11 9" fill="none"><path d="M1 4.5L4 7.5L10 1.5" stroke="#fff" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>';
      el.appendChild(mark);
    },
    fillCheck(text) {
      this.$refs.editorEl.textContent = text;
      this.editorDraft = text;
      this.charCount = Math.min(text.length, 500);
      this.showResult = false;
      this.noErrors = false;
      this.bubble = null;
      this.favSent = false;
      this.favSentId = null;
      this.openPop = null;
    },
    async clearCheckHistory() {
      try {
        await API.clearCheckHistory();
        this.checkHistory = [];
      } catch (e) { this.showToast(e); }
    },
    startCountdown() {
      clearInterval(this.cdTimer);
      this.cdRunning = true;
      this.cdRemain = 5;
      this.cdTimer = setInterval(() => {
        this.cdRemain = +(this.cdRemain - 0.1).toFixed(1);
        if (this.cdRemain <= 0) this.finishAdd();
      }, 100);
    },
    /* 倒计时中点击 = 取消加入 */
    addWrongClick() {
      clearInterval(this.cdTimer);
      this.cdRunning = false;
    },
    /* 倒计时结束：POST /api/wrong-words 加入错词本（§1.4） */
    async finishAdd() {
      clearInterval(this.cdTimer);
      this.cdRunning = false;
      if (!this.checkResultData || this.noErrors) return;
      const err = (this.checkResultData.errors && this.checkResultData.errors[0]) || null;
      let word = err && err.fix ? err.fix.split('→')[0].trim() : '';
      let corrected = this.currentSentence;
      if (word) {
        const right = err.fix.split('→')[1] ? err.fix.split('→')[1].trim() : '';
        corrected = right ? this.currentSentence.replace(word, right) : this.currentSentence;
      }
      try {
        await API.addWrongWord({
          word: word || this.currentSentence.slice(0, 50),
          error_type: err ? err.type : '',
          original_sentence: this.currentSentence,
          corrected_sentence: corrected,
          analysis_json: this.checkResultData.struct ? JSON.stringify(this.checkResultData.struct) : ''
        });
        this.loadWrongWords();
      } catch (e) { this.showToast(e); }
    },

    /* ================= 仓库页 ================= */
    toggleType(name) {
      this.activeType = this.activeType === name ? null : name;
    },
    pickRange(label) {
      this.dateRange = label;
      this.dateOpen = false;
    },
    /* ---- 编辑模式：删除单条记录 ---- */
    toggleRepoEdit() {
      this.repoEdit = !this.repoEdit;
      if (!this.repoEdit) this.cancelDel();
    },
    /* 删除角标统一入口：第一次点进入确认态，再点执行（4s 未确认自动还原） */
    onDel(kind, item) {
      const key = kind + item.id;
      if (this.delConfirmKey !== key) {
        this.delConfirmKey = key;
        clearTimeout(this.delTimer);
        this.delTimer = setTimeout(() => { this.delConfirmKey = ''; }, 4000);
        return;
      }
      this.cancelDel();
      if (kind === 'w') this.removeWrong(item);
      else if (kind === 's') this.removeSent(item);
      else this.removeDisc(item);
    },
    cancelDel() {
      clearTimeout(this.delTimer);
      this.delConfirmKey = '';
    },
    async removeWrong(w) {
      try {
        await API.deleteWrongWord(w.id);
        this.wrongWords = this.wrongWords.filter(x => x.id !== w.id);
        this.rebuildTypeList();
        this.showToast('已删除词条「' + (w.word || w.bad) + '」', 'success');
      } catch (e) { this.showToast(e); }
    },
    async removeSent(s) {
      try {
        await API.deleteSentence(s.id);
        this.sentences = this.sentences.filter(x => x.id !== s.id);
        this.showToast('已删除例句', 'success');
      } catch (e) { this.showToast(e); }
    },
    async removeDisc(d) {
      try {
        await API.deleteDiscrimination(d.id);
        this.discPairs = this.discPairs.filter(x => x.id !== d.id);
        /* 历史卡片同步裁剪 */
        this.discHistory = this.discHistory.filter(x => x.id !== d.id);
        this.showToast('已删除辨析「' + d.word + '」', 'success');
      } catch (e) { this.showToast(e); }
    },
    /* 结构分析弹窗：解析记录的 analysis_json（struct 段），无则只展示基础信息 */
    openWrongModal(w) {
      const struct = this.parseJSON(w.analysisJson);
      this.structModal = {
        sub: w.corrected || w.original, struct,
        word: w.word, errorType: w.errorType,
        original: w.original, corrected: w.corrected
      };
      this.activeModal = 'struct';
    },
    openSentModal(s) {
      const struct = this.parseJSON(s.analysisJson);
      this.structModal = { sub: s.en, struct, word: '', errorType: '', original: s.en, corrected: '' };
      this.activeModal = 'struct';
    },
    /* 场景辨析弹窗：解析记录的 result_json（sub/core/words，契约同 §3.1） */
    openSceneModal(d) {
      const result = this.parseJSON(d.resultJson);
      this.sceneModalData = result
        ? { sub: result.sub || d.word, core: result.core || '', words: result.words || [] }
        : { sub: d.word, core: '', words: [] };
      this.activeModal = 'scene';
    },

    /* ================= 趋势页 ================= */
    pickTrendType(name) {
      this.trendType = name;
      this.ddOpen = false;
      /* 后端 type 筛选已接入（api/README.md §5.1），切换后按类型重算折线 */
      this.loadTrends();
    },
    async loadTrends() {
      try {
        const typeParam = this.trendType === '全部错误类型' ? '' : this.trendType;
        const resp = await API.trends(14, typeParam);
        const pts = resp.points || [];
        /* 口径：summary 直接采用后端数值；前端构建 14 天完整时间轴——
           无记录日期不产生数据点（acc=null），有值点之间直连画折线 */
        const byDate = {};
        pts.forEach(p => { byDate[p.date] = p.accuracy; });
        const list = [];
        for (let i = 13; i >= 0; i--) {
          const d = new Date(); d.setHours(0, 0, 0, 0); d.setDate(d.getDate() - i);
          const key = d.getFullYear() + '-' + String(d.getMonth() + 1).padStart(2, '0') + '-' + String(d.getDate()).padStart(2, '0');
          const raw = byDate[key];
          const acc = raw == null ? null : Math.min(100, Math.max(0, raw));
          list.push({
            label: key.slice(5),
            acc,
            hasValue: acc != null,
            /* x 按 14 天均分，今日恒在最右端 */
            x: +(50 + (13 - i) * (1055 - 50) / 13).toFixed(1)
          });
        }
        /* 纵轴自适应（整洁刻度）：量程覆盖全部数据点并留边距，
           步长从 1/2/5/10/20/25 中选取，保证 4 等分刻度都是整洁数值；
           数据跨度过小（单点/平线）时扩到 ≥20%，上限恒 ≤100% */
        const vals = list.filter(p => p.hasValue).map(p => p.acc);
        let yMin = 0, yMax = 100;
        if (vals.length) {
          const dMin = Math.min.apply(null, vals), dMax = Math.max.apply(null, vals);
          const pad = Math.max(2, (dMax - dMin) * 0.08);
          let lo = dMin - pad, hi = dMax + pad;
          if (hi - lo < 20) { const c = (lo + hi) / 2; lo = c - 10; hi = c + 10; }
          if (hi > 100) hi = 100;
          if (lo < 0) lo = 0;
          const span = hi - lo;
          let step = 25;
          for (const s of [1, 2, 5, 10, 20, 25]) {
            if (s * 4 >= span) { step = s; break; }
          }
          yMin = Math.max(0, Math.floor(lo / step) * step);
          yMax = yMin + step * 4;
          if (yMax > 100) { yMax = 100; yMin = Math.max(0, 100 - step * 4); }
        }
        this.trendScale = { min: yMin, max: yMax };
        this.trendPoints = list.map(p => ({
          label: p.label,
          hasValue: p.hasValue,
          x: p.x,
          /* y 映射：量程顶部 → 20，量程底部 → 270（与动态网格一致） */
          y: p.acc == null ? null : +(270 - (p.acc - yMin) / (yMax - yMin) * 250).toFixed(1)
        }));
        const valid = this.trendPoints.filter(p => p.hasValue);
        this.trendHasData = valid.length > 0;
        const s = resp.summary || {};
        this.statToday = s.today == null ? 0 : Math.round(s.today);
        this.statAvg = Math.round(s.avg || 0);
        this.statUp = Math.round(s.delta || 0);
      } catch (e) { this.showToast(e); }
    },
    /* 统计数字滚动动画 */
    animateTrendStats() {
      if (this.page !== 'trend') return;
      this.tween('statToday', this.statToday, 900);
      this.tween('statAvg', this.statAvg, 900);
      this.tween('statUp', this.statUp, 900);
    },
    tween(key, to, duration) {
      const from = this[key] || 0;
      const start = performance.now();
      const step = (now) => {
        const p = Math.min((now - start) / duration, 1);
        const eased = 1 - Math.pow(1 - p, 3);
        this[key] = Math.round(from + (to - from) * eased);
        if (p < 1) requestAnimationFrame(step);
      };
      requestAnimationFrame(step);
    },

    /* ================= 辨析 / 表达 ================= */
    async doDiscriminate() {
      this.closeAllPops();
      const word = this.discInput.trim();
      if (!word) { this.showToast('请先输入要辨析的单词', 'info'); return; }
      if (this.discBusy) return;
      this.discBusy = true;
      this.favDisc = false;
      try {
        /* reuse_history 与配置页「辨析复用历史记录」联动（§3.1） */
        const resp = await API.discriminate(word, this.reuseOn);
        this.discResultData = resp;
        this.$nextTick(() => this.$refs.discResult && this.$refs.discResult.scrollIntoView({ behavior: 'smooth', block: 'nearest' }));
        this.loadDiscriminations();
      } catch (e) {
        this.showLLMError(e);
      } finally {
        this.discBusy = false;
      }
    },
    reuseDisc(word) {
      this.discInput = word;
      this.openPop = null;
      this.doDiscriminate();
    },
    async doExpress() {
      this.closeAllPops();
      const text = this.expInput.trim();
      if (!text) { this.showToast('请先输入要翻译的中文', 'info'); return; }
      if (this.expBusy) return;
      this.expBusy = true;
      this.favExp = false;
      try {
        const resp = await API.express(text);
        this.expResultData = resp;
        this.$nextTick(() => this.$refs.expResult && this.$refs.expResult.scrollIntoView({ behavior: 'smooth', block: 'nearest' }));
        this.loadExpHistory();
      } catch (e) {
        this.showLLMError(e);
      } finally {
        this.expBusy = false;
      }
    },
    reuseExp(text) {
      this.expInput = text;
      this.openPop = null;
      this.doExpress();
    },
    /* 收藏当前辨析结果：占位响应不带 id → 按 word 从历史列表反查最新记录 */
    async toggleFavDisc() {
      const on = !this.favDisc;
      try {
        const id = await this.resolveDiscId();
        if (!id) { this.showToast('暂无可收藏的记录（需先成功完成一次辨析）', 'info'); return; }
        await API.favDiscrimination(id, on);
        this.favDisc = on;
        this.loadDiscriminations();
      } catch (e) { this.showToast(e); }
    },
    async resolveDiscId() {
      const word = this.discInput.trim();
      const hit = this.discPairs.find(d => d.word === word);
      return hit ? hit.id : null;
    },
    /* 收藏当前表达结果：按中文原文从表达历史反查最新记录 */
    async toggleFavExp() {
      const on = !this.favExp;
      try {
        const id = await this.resolveExpId();
        if (!id) { this.showToast('暂无可收藏的记录（需先成功完成一次翻译）', 'info'); return; }
        await API.favExpression(id, on);
        this.favExp = on;
        this.loadExpHistory();
      } catch (e) { this.showToast(e); }
    },
    async resolveExpId() {
      const cn = this.expInput.trim();
      try {
        const list = await API.expressionHistory(50);
        const hit = (list || []).find(h => h.chinese === cn);
        return hit ? hit.id : null;
      } catch (e) { this.showToast(e); return null; }
    },
    async clearDiscHistory() {
      try {
        await API.clearDiscHistory();
        this.discHistory = [];
        this.loadDiscriminations();
      } catch (e) { this.showToast(e); }
    },
    async clearExpHistory() {
      try {
        await API.clearExpHistory();
        this.expHistory = [];
      } catch (e) { this.showToast(e); }
    },

    /* ================= 弹窗 ================= */
    openModal(id) { this.activeModal = id; },
    closeModal() { this.activeModal = null; },

    /* ================= 成分说明弹窗 ================= */
    /* 角色归一化：精确 → 「·」分段逐个尝试 → 默认兜底（api/README.md §7.1） */
    normalizeRole(role) {
      const segs = String(role || '').split('·').map(s => s.trim()).filter(Boolean);
      return segs.length ? segs : [''];
    },
    async openComp(role) {
      this.compModal = { role, info: null, example: '', loading: false };
      this.activeModal = 'comp';
      /* 分段尝试：名词短语 · 宾语 → ['名词短语', '宾语'] */
      const segs = this.normalizeRole(role);
      for (const seg of segs) {
        try {
          this.compModal.info = await API.compInfo(seg);
          return;
        } catch (e) {
          if (e.code === 'network_error') { this.showToast(e); this.compModal.info = DEFAULT_COMP_INFO; return; }
          /* 404 / 其他 → 尝试下一段 */
        }
      }
      this.compModal.info = DEFAULT_COMP_INFO;
    },
    /* LLM 生成用法示例；占位期失败回退 DB 预置 demo（api/README.md §7.2） */
    async requestExample() {
      if (this.compModal.loading) return;
      this.compModal.loading = true;
      try {
        const resp = await API.usageExample(this.compModal.role, this.currentSentence || this.compModal.role);
        this.compModal.example = resp.example;
      } catch (e) {
        this.showToast(e);
        this.compModal.example = this.compModal.info ? this.compModal.info.demo : '';
      } finally {
        this.compModal.loading = false;
      }
    }
  },

  mounted() {
    this.$nextTick(() => {
      this.updateNavGlider();
      this.updateTabGlider();
    });
    window.addEventListener('resize', () => {
      this.updateNavGlider();
      this.updateTabGlider();
    });

    /* Ctrl + Enter 快速检查 */
    window.addEventListener('keydown', (e) => {
      if (e.ctrlKey && e.key === 'Enter') this.doCheck();
      if (e.key === 'Escape') { this.closeModal(); this.closeAllPops(); }
    });

    /* 点击空白处关闭弹层 */
    document.addEventListener('click', (e) => {
      if (!e.target.closest('.history-pop') && !e.target.closest('.btn-history') && !e.target.closest('.dropdown')) {
        this.closeAllPops();
      }
    });
    this.onEditorInput();
    /* 首屏：并行拉取真实数据 */
    this.boot();
  }
});

/* ====== 涟漪指令：按钮点击的水波反馈 ====== */
app.directive('ripple', {
  mounted(el, binding) {
    el.classList.add('ripple-host');
    const dark = !!binding.modifiers.dark;
    el.addEventListener('pointerdown', (e) => {
      const rect = el.getBoundingClientRect();
      const d = Math.max(rect.width, rect.height);
      const s = document.createElement('span');
      s.className = 'ripple' + (dark ? ' dark' : '');
      s.style.width = s.style.height = d + 'px';
      s.style.left = (e.clientX - rect.left - d / 2) + 'px';
      s.style.top = (e.clientY - rect.top - d / 2) + 'px';
      el.appendChild(s);
      setTimeout(() => s.remove(), 650);
    });
  }
});

app.mount('#app');
