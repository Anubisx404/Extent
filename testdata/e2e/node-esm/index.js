import express from "express";

const app = express();

app.get("/health", (req, res) => {
  res.json({ status: "ok" });
});

const port = Number(process.env.PORT) || 8080;
app.listen(port, () => console.log(`listening on ${port}`));
