import React from 'react';
import { createRoot } from 'react-dom/client';
import './styles/querycraft.css';
import App from './App';
import { StoreProvider } from './state/store';
import { resolveApiMode } from './api/index';

const container = document.getElementById('root');
if (!container) throw new Error('Root container #root not found');

const root = createRoot(container);

async function bootstrap(): Promise<void> {
  await resolveApiMode();
  root.render(
    <React.StrictMode>
      <StoreProvider>
        <App />
      </StoreProvider>
    </React.StrictMode>
  );
}

void bootstrap();
