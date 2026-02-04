# Superbowl LX Prop Pool

A simple prop pool application for Superbowl LX.

## Running the Application

Prerequisites:
- Go
- SQLite (usually included with Go drivers)

To run the application locally:

```bash
go run .
```

The application will start on port 4884 (default) using a local `game.db` SQLite database.

Access the application at: http://localhost:4884

## Admin Interface

To manage the game state and resolve questions, navigate to:

http://localhost:4884/admin?key=touchdown

From here you can:
- Lock/Unlock the game (Open/Locked status).
- Resolve questions (mark correct answers).
- Refresh questions from `questions.json`.
- Manage users (reset PINs, delete users, update rooms).

## Configuration

- `PORT`: Environment variable to set the port (default: 4884).
- `DB_NAME`: Environment variable to set the database file path (default: ./game.db).
