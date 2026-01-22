import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './index.css'
import App from './App.tsx'
import liff from '@line/liff' // LIFFをインポート

const liffId = '2008948547-cGpAsaua'; // きみのLIFF ID

liff.init({ liffId: liffId })
  .then(() => {
    console.log('LIFF init succeeded.');
    createRoot(document.getElementById('root')!).render(
      <StrictMode>
        <App />
      </StrictMode>,
    );
  })
  .catch((e: Error) => {
    console.error('LIFF init failed.', e.message);
    // 初期化失敗時のUI表示などをここに追加
    createRoot(document.getElementById('root')!).render(
      <StrictMode>
        <div>LIFFの初期化に失敗しました: {e.message}</div>
      </StrictMode>,
    );
  });
