import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { HeroUIProvider } from '@heroui/react'
import './index.css'
import App from './App.tsx'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <HeroUIProvider>
      <main className="light text-foreground bg-background min-h-screen">
        <App />
      </main>
    </HeroUIProvider>
  </StrictMode>,
)
