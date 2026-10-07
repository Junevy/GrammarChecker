/* 服务器地址输入窗的预加载桥：页面只能调用 submit()，无 Node 能力 */
const { contextBridge, ipcRenderer } = require('electron');

contextBridge.exposeInMainWorld('shell', {
  submit: (value) => ipcRenderer.send('input-submit', value),
});
