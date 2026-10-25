import { useState } from 'react'

function App() {
  const [text, setText] = useState('')

  return (
    <main className="page">
      <form className="entry-form" onSubmit={(event) => event.preventDefault()}>
        <label className="visually-hidden" htmlFor="text-input">
          Text
        </label>
        <input
          id="text-input"
          type="text"
          value={text}
          onChange={(event) => setText(event.target.value)}
          placeholder="Enter text"
          autoComplete="off"
        />
        <button type="submit">Submit</button>
      </form>
    </main>
  )
}

export default App
